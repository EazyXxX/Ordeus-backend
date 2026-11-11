package diary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	_ "golang.org/x/image/webp"

	"ordeus/backend/internal/ai"
	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
)

const (
	maxNoteImages = 10
	// The browser shrinks photos before sending them, so this is only a ceiling.
	maxImageBytes = 10 << 20
)

// SVG is left out on purpose: it can carry scripts.
var imageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

var (
	errTooManyImages = errors.New("a note holds at most 10 images")
	errNotAnImage    = errors.New("not a supported image")
)

// imageFiles keeps uploaded pictures on the server's disk, one folder per
// account. A cloud store can replace it later behind the same three methods.
type imageFiles struct {
	dir string
}

func (f imageFiles) path(userID, id string) string {
	return filepath.Join(f.dir, userID, id)
}

// save writes to a temporary file first, so a broken upload never leaves half
// a picture behind under its real name.
func (f imageFiles) save(userID, id string, data []byte) error {
	folder := filepath.Join(f.dir, userID)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(folder, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), f.path(userID, id))
}

func (f imageFiles) read(userID, id string) ([]byte, error) {
	return os.ReadFile(f.path(userID, id))
}

func (f imageFiles) remove(userID string, ids []string) {
	for _, id := range ids {
		if err := os.Remove(f.path(userID, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("remove image", "id", id, "error", err)
		}
	}
}

func (s *Service) uploadImage(w http.ResponseWriter, r *http.Request) {
	noteID, ok := httpjson.PathID(w, r)
	if !ok {
		return
	}
	data, contentType, err := readImage(w, r)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	userID := auth.UserID(r.Context())
	id, err := s.store.addImage(r.Context(), userID, noteID, contentType, len(data), func(id string) error {
		return s.files.save(userID, id, data)
	})
	switch {
	case errors.Is(err, errTooManyImages):
		httpjson.Error(w, http.StatusConflict, err.Error())
	case err != nil:
		fail(w, r, err)
	default:
		httpjson.Write(w, http.StatusCreated, map[string]string{"id": id})
	}
}

// upload is a picture read from a request and checked.
type upload struct {
	data        []byte
	contentType string
}

// readImage takes the "file" field of a multipart form.
func readImage(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+1<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		return nil, "", errors.New("expected an image in the file field")
	}
	defer file.Close()
	return readImageFile(file)
}

// readImageFile checks by the contents, not by the name, that a file is a
// picture the app shows.
func readImageFile(file multipart.File) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
	if err != nil {
		return nil, "", errors.New("could not read the image")
	}
	if len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("an image must be at most %d MB", maxImageBytes>>20)
	}
	contentType := http.DetectContentType(data)
	if !imageTypes[contentType] {
		return nil, "", errNotAnImage
	}
	return data, contentType, nil
}

func (s *Service) serveImage(w http.ResponseWriter, r *http.Request) {
	id, ok := httpjson.PathID(w, r)
	if !ok {
		return
	}
	userID := auth.UserID(r.Context())
	var contentType string
	var createdAt time.Time
	err := s.store.pool.QueryRow(r.Context(), `
		SELECT content_type, created_at FROM diary_note_images WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&contentType, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		httpjson.Error(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}

	data, err := os.ReadFile(s.files.path(userID, id))
	if err != nil {
		slog.Error("read image", "id", id, "error", err)
		httpjson.Error(w, http.StatusNotFound, "not found")
		return
	}
	// An image never changes under its id, so the browser may keep it.
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeContent(w, r, "", createdAt, bytes.NewReader(data))
}

func (s *Service) deleteImage(w http.ResponseWriter, r *http.Request) {
	id, ok := httpjson.PathID(w, r)
	if !ok {
		return
	}
	userID := auth.UserID(r.Context())
	tag, err := s.store.pool.Exec(r.Context(),
		`DELETE FROM diary_note_images WHERE id = $1 AND user_id = $2`, id, userID)
	if err == nil && tag.RowsAffected() > 0 {
		s.files.remove(userID, []string{id})
	}
	finish(w, r, requireRow(tag.RowsAffected(), err))
}

// addImage records an image and stores its file in one step: if the file
// cannot be written, the row goes away too. Locking the note keeps two uploads
// at once from passing the limit together.
func (s *store) addImage(ctx context.Context, userID, noteID, contentType string, size int, write func(id string) error) (string, error) {
	id := uuid.Must(uuid.NewV7()).String()
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var count int
		err := tx.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM diary_note_images WHERE note_id = n.id)
			FROM diary_notes n WHERE n.id = $1 AND n.user_id = $2 FOR UPDATE`, noteID, userID).Scan(&count)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return err
		}
		if count >= maxNoteImages {
			return errTooManyImages
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO diary_note_images (id, note_id, user_id, content_type, size_bytes) VALUES ($1, $2, $3, $4, $5)`,
			id, noteID, userID, contentType, size)
		if err != nil {
			return err
		}
		return write(id)
	})
	return id, err
}

func (s *store) noteImages(ctx context.Context, userID, noteID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text FROM diary_note_images WHERE note_id = $1 AND user_id = $2`, noteID, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

type storedImage struct {
	id, contentType string
}

func (s *store) captureImages(ctx context.Context, captureID string) ([]storedImage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, content_type FROM diary_note_images WHERE capture_id = $1 ORDER BY id`, captureID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (storedImage, error) {
		var image storedImage
		return image, row.Scan(&image.id, &image.contentType)
	})
}

func (s *store) failedCaptureImages(ctx context.Context, userID, captureID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id::text FROM diary_note_images i
		JOIN diary_captures c ON c.id = i.capture_id
		WHERE c.id = $1 AND c.user_id = $2 AND c.status = 'failed'`, captureID, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// modelImages prepares the first pictures of a capture for the model. Models
// read JPEG and PNG for sure, so WebP and GIF are redrawn as JPEG. A picture
// that cannot be read is skipped: the note is still made from the rest.
func (f imageFiles) modelImages(userID string, images []storedImage) []ai.Image {
	result := make([]ai.Image, 0, min(len(images), maxModelImages))
	for _, stored := range images {
		if len(result) == maxModelImages {
			break
		}
		data, err := f.read(userID, stored.id)
		if err != nil {
			slog.Warn("read capture image", "id", stored.id, "error", err)
			continue
		}
		if stored.contentType == "image/jpeg" || stored.contentType == "image/png" {
			result = append(result, ai.Image{ContentType: stored.contentType, Data: data})
			continue
		}
		picture, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			slog.Warn("decode capture image", "id", stored.id, "error", err)
			continue
		}
		var converted bytes.Buffer
		if err := jpeg.Encode(&converted, picture, &jpeg.Options{Quality: 85}); err != nil {
			continue
		}
		result = append(result, ai.Image{ContentType: "image/jpeg", Data: converted.Bytes()})
	}
	return result
}
