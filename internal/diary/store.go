package diary

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	listLimit = 500
	// The book reads the whole diary, so daily notes go far beyond one screen.
	dailyListLimit      = 10_000
	contextNotesLimit   = 50
	uniqueViolationCode = "23505"
)

// New folders take the next color, so they stay easy to tell apart on the graph.
var folderColorRotation = []string{"red", "orange", "yellow", "green", "blue", "purple", "pink"}

var (
	errNotFound = errors.New("not found")
	errConflict = errors.New("conflict")
)

type NoteRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Note struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Body   string    `json:"body"`
	Folder string    `json:"folder"`
	Tags   []string  `json:"tags"`
	Links  []NoteRef `json:"links"`
	// Images are ids, oldest first; each is served at /api/diary/images/{id}.
	Images    []string  `json:"images"`
	FromAI    bool      `json:"fromAi"`
	DailyDate *string   `json:"dailyDate"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Folder struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Emoji string `json:"emoji"`
	Notes int    `json:"notes"`
}

type Tag struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Emoji string `json:"emoji"`
	Notes int    `json:"notes"`
}

type Capture struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"createdAt"`
	// Images counts the pictures sent with the capture.
	Images int `json:"images"`
}

type NoteFilter struct {
	Daily    bool
	FolderID string
	Tag      string
	Query    string
	// From and To bound the day a daily note describes, both inclusive.
	From string
	To   string
}

type NoteInput struct {
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Folder  string   `json:"folder"`
	Tags    []string `json:"tags"`
	LinkIDs []string `json:"linkIds"`
	// DailyDate marks a daily note and names the day it describes.
	DailyDate *string `json:"dailyDate"`
}

type store struct {
	pool *pgxpool.Pool
}

func (s *store) notes(ctx context.Context, userID string, filter NoteFilter) ([]Note, error) {
	limit := listLimit
	if filter.Daily {
		limit = dailyListLimit
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id::text, n.title, n.body, COALESCE(f.name, ''),
			COALESCE((
				SELECT array_agg(t.name ORDER BY t.name)
				FROM diary_note_tags nt JOIN diary_tags t ON t.id = nt.tag_id
				WHERE nt.note_id = n.id
			), '{}'::text[]),
			COALESCE((
				SELECT jsonb_agg(jsonb_build_object('id', other.id, 'title', other.title) ORDER BY other.title)
				FROM diary_note_links l
				JOIN diary_notes other
					ON other.id = CASE WHEN l.note_a = n.id THEN l.note_b ELSE l.note_a END
				WHERE n.id IN (l.note_a, l.note_b)
			), '[]'::jsonb),
			COALESCE((
				SELECT array_agg(i.id::text ORDER BY i.id) FROM diary_note_images i WHERE i.note_id = n.id
			), '{}'::text[]),
			n.capture_id IS NOT NULL, to_char(n.daily_date, 'YYYY-MM-DD'), n.created_at, n.updated_at
		FROM diary_notes n
		LEFT JOIN diary_folders f ON f.id = n.folder_id
		WHERE n.user_id = $5
			AND (NOT $6 OR n.daily_date IS NOT NULL)
			AND (NULLIF($7, '') IS NULL OR n.daily_date >= NULLIF($7, '')::date)
			AND (NULLIF($8, '') IS NULL OR n.daily_date <= NULLIF($8, '')::date)
			AND (NULLIF($1, '') IS NULL OR n.folder_id = NULLIF($1, '')::uuid)
			AND ($2 = '' OR EXISTS (
				SELECT 1 FROM diary_note_tags nt JOIN diary_tags t ON t.id = nt.tag_id
				WHERE nt.note_id = n.id AND t.name = $2
			))
			AND ($3 = '' OR strpos(lower(n.title || ' ' || n.body), lower($3)) > 0)
		ORDER BY CASE WHEN $6 THEN n.daily_date END DESC, n.id DESC
		LIMIT $4`, filter.FolderID, filter.Tag, filter.Query, limit, userID, filter.Daily, filter.From, filter.To)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Note, error) {
		var note Note
		err := row.Scan(&note.ID, &note.Title, &note.Body, &note.Folder, &note.Tags, &note.Links, &note.Images,
			&note.FromAI, &note.DailyDate, &note.CreatedAt, &note.UpdatedAt)
		return note, err
	})
}

func (s *store) folders(ctx context.Context, userID string) ([]Folder, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT f.id::text, f.name, f.color, COALESCE(f.emoji, ''), count(n.id)::int
		FROM diary_folders f
		LEFT JOIN diary_notes n ON n.folder_id = f.id
		WHERE f.user_id = $1
		GROUP BY f.id
		ORDER BY lower(f.name)`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Folder])
}

func (s *store) tags(ctx context.Context, userID string) ([]Tag, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id::text, t.name, t.color, COALESCE(t.emoji, ''), count(*)::int
		FROM diary_tags t
		JOIN diary_note_tags nt ON nt.tag_id = t.id
		WHERE t.user_id = $1
		GROUP BY t.id
		ORDER BY count(*) DESC, t.name`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Tag])
}

func (s *store) createNote(ctx context.Context, userID string, input NoteInput) (string, error) {
	var id string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		folderID, err := ensureFolder(ctx, tx, userID, input.Folder)
		if err != nil {
			return err
		}
		id, err = insertNote(ctx, tx, userID, input.Title, input.Body, folderID, nil, input.DailyDate)
		if err != nil {
			return err
		}
		if err := addTags(ctx, tx, userID, id, input.Tags); err != nil {
			return err
		}
		return addLinks(ctx, tx, userID, id, input.LinkIDs)
	})
	return id, err
}

func (s *store) updateNote(ctx context.Context, userID, id string, input NoteInput) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		folderID, err := ensureFolder(ctx, tx, userID, input.Folder)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE diary_notes SET title = $3, body = $4, folder_id = $5, daily_date = $6::date
			WHERE id = $1 AND user_id = $2`,
			id, userID, input.Title, input.Body, folderID, input.DailyDate)
		if err := requireRow(tag.RowsAffected(), err); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM diary_note_tags WHERE note_id = $1`, id); err != nil {
			return err
		}
		if err := addTags(ctx, tx, userID, id, input.Tags); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM diary_note_links WHERE note_a = $1 OR note_b = $1`, id); err != nil {
			return err
		}
		return addLinks(ctx, tx, userID, id, input.LinkIDs)
	})
}

func (s *store) deleteNote(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_notes WHERE id = $1 AND user_id = $2`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

// updateFolder changes only the fields that are not nil.
func (s *store) updateFolder(ctx context.Context, userID, id string, name, color, emoji *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE diary_folders
		SET name = COALESCE($3, name), color = COALESCE($4, color), emoji = COALESCE($5, emoji)
		WHERE id = $1 AND user_id = $2`, id, userID, name, color, emoji)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
		return errConflict
	}
	return requireRow(tag.RowsAffected(), err)
}

// updateTag changes only the fields that are not nil.
func (s *store) updateTag(ctx context.Context, userID, id string, color, emoji *string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE diary_tags SET color = COALESCE($3, color), emoji = COALESCE($4, emoji)
		WHERE id = $1 AND user_id = $2`, id, userID, color, emoji)
	return requireRow(tag.RowsAffected(), err)
}

// deleteFolder keeps the notes: they simply lose their folder.
// deleteTag removes the tag from every note, and the notes themselves stay.
func (s *store) deleteTag(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_tags WHERE id = $1 AND user_id = $2`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) deleteFolder(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_folders WHERE id = $1 AND user_id = $2`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

// createCapture stores the text and its pictures in one step, so the worker
// never takes a capture whose pictures are still on their way.
func (s *store) createCapture(ctx context.Context, userID, text, writtenOn string, images []upload, files imageFiles) (Capture, error) {
	capture := Capture{Text: text, Status: statusPending, Images: len(images)}
	var written []string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`INSERT INTO diary_captures (user_id, raw_text, written_on) VALUES ($1, $2, $3::date) RETURNING id::text, created_at`,
			userID, text, writtenOn,
		).Scan(&capture.ID, &capture.CreatedAt)
		if err != nil {
			return err
		}
		for _, image := range images {
			id := uuid.Must(uuid.NewV7()).String()
			_, err := tx.Exec(ctx, `
				INSERT INTO diary_note_images (id, capture_id, user_id, content_type, size_bytes) VALUES ($1, $2, $3, $4, $5)`,
				id, capture.ID, userID, image.contentType, len(image.data))
			if err != nil {
				return err
			}
			if err := files.save(userID, id, image.data); err != nil {
				return err
			}
			written = append(written, id)
		}
		return nil
	})
	if err != nil {
		files.remove(userID, written)
	}
	return capture, err
}

// unfinishedCaptures returns everything not yet turned into notes.
func (s *store) unfinishedCaptures(ctx context.Context, userID string) ([]Capture, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, raw_text, status, error, created_at,
			(SELECT count(*)::int FROM diary_note_images i WHERE i.capture_id = diary_captures.id)
		FROM diary_captures
		WHERE status <> 'done' AND user_id = $1
		ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Capture])
}

func (s *store) retryCapture(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE diary_captures SET status = 'pending', error = ''
		WHERE id = $1 AND user_id = $2 AND status = 'failed'`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) deleteFailedCapture(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM diary_captures WHERE id = $1 AND user_id = $2 AND status = 'failed'`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

// requeueInterrupted returns captures left mid-flight by a stopped server.
func (s *store) requeueInterrupted(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE diary_captures SET status = 'pending' WHERE status = 'processing'`)
	return err
}

type claimedCapture struct {
	id, text, userID, writtenOn string
}

// claimCapture takes the next capture along with the account that wrote it. A
// capture without an owner waits until the first account claims it.
func (s *store) claimCapture(ctx context.Context) (claimedCapture, error) {
	var capture claimedCapture
	err := s.pool.QueryRow(ctx, `
		UPDATE diary_captures SET status = 'processing'
		WHERE id = (
			SELECT id FROM diary_captures WHERE status = 'pending' AND user_id IS NOT NULL
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, raw_text, user_id::text, to_char(written_on, 'YYYY-MM-DD')`,
	).Scan(&capture.id, &capture.text, &capture.userID, &capture.writtenOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedCapture{}, errNotFound
	}
	return capture, err
}

func (s *store) failCapture(ctx context.Context, id, message string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE diary_captures SET status = 'failed', error = $2 WHERE id = $1`, id, message)
	return err
}

func (s *store) organizerContext(ctx context.Context, userID string) (organizerContext, error) {
	var known organizerContext
	if err := s.pool.QueryRow(ctx, `SELECT language FROM users WHERE id = $1`, userID).Scan(&known.Language); err != nil {
		return known, err
	}
	rows, err := s.pool.Query(ctx, `SELECT name FROM diary_folders WHERE user_id = $1 ORDER BY lower(name)`, userID)
	if err != nil {
		return known, err
	}
	if known.Folders, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return known, err
	}
	rows, err = s.pool.Query(ctx, `
		SELECT DISTINCT t.name
		FROM diary_tags t JOIN diary_note_tags nt ON nt.tag_id = t.id
		WHERE t.user_id = $1
		ORDER BY t.name`, userID)
	if err != nil {
		return known, err
	}
	if known.Tags, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return known, err
	}
	rows, err = s.pool.Query(ctx,
		`SELECT id::text, title FROM diary_notes WHERE user_id = $1 ORDER BY id DESC LIMIT $2`, userID, contextNotesLimit)
	if err != nil {
		return known, err
	}
	known.Notes, err = pgx.CollectRows(rows, pgx.RowToStructByPos[NoteRef])
	return known, err
}

// applyPlan stores a whole plan atomically and finishes the capture.
func (s *store) applyPlan(ctx context.Context, userID, captureID string, result plan, known []NoteRef) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ids := make([]string, len(result.Notes))
		for i, note := range result.Notes {
			folderID, err := ensureFolder(ctx, tx, userID, note.Folder)
			if err != nil {
				return err
			}
			var dailyDate *string
			if note.DailyDate != "" {
				dailyDate = &note.DailyDate
			}
			if ids[i], err = insertNote(ctx, tx, userID, note.Title, note.Body, folderID, &captureID, dailyDate); err != nil {
				return err
			}
			if err := addTags(ctx, tx, userID, ids[i], note.Tags); err != nil {
				return err
			}
			if err := suggestEmojis(ctx, tx, userID, folderID, note); err != nil {
				return err
			}
		}
		// The pictures go to the first note: a split capture rarely has a
		// picture for each part, and the author can move them.
		if _, err := tx.Exec(ctx, `
			UPDATE diary_note_images SET note_id = $1, capture_id = NULL WHERE capture_id = $2`, ids[0], captureID); err != nil {
			return err
		}
		for i, note := range result.Notes {
			links := make([]string, 0, len(note.RelatedExisting)+len(note.RelatedNew))
			for _, number := range note.RelatedExisting {
				links = append(links, known[number-1].ID)
			}
			for _, index := range note.RelatedNew {
				links = append(links, ids[index])
			}
			if err := addLinks(ctx, tx, userID, ids[i], links); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx,
			`UPDATE diary_captures SET status = 'done', error = '', processed_at = now() WHERE id = $1`, captureID)
		return err
	})
}

// suggestEmojis fills only what no one has chosen, so the author's choice and
// removal both stay.
func suggestEmojis(ctx context.Context, tx pgx.Tx, userID string, folderID *string, note plannedNote) error {
	if folderID != nil && note.FolderEmoji != "" {
		if _, err := tx.Exec(ctx,
			`UPDATE diary_folders SET emoji = $2 WHERE id = $1 AND emoji IS NULL`, *folderID, note.FolderEmoji); err != nil {
			return err
		}
	}
	for _, item := range note.TagEmojis {
		if _, err := tx.Exec(ctx,
			`UPDATE diary_tags SET emoji = $3 WHERE user_id = $1 AND name = $2 AND emoji IS NULL`,
			userID, item.Tag, item.Emoji); err != nil {
			return err
		}
	}
	return nil
}

func ensureFolder(ctx context.Context, tx pgx.Tx, userID, name string) (*string, error) {
	if name == "" {
		return nil, nil
	}
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO diary_folders (user_id, name, color)
		SELECT $1, $2, ($3::text[])[count(*) % cardinality($3::text[]) + 1]
		FROM diary_folders WHERE user_id = $1
		ON CONFLICT (user_id, lower(name)) DO UPDATE SET name = diary_folders.name
		RETURNING id::text`, userID, name, folderColorRotation).Scan(&id)
	return &id, err
}

func insertNote(ctx context.Context, tx pgx.Tx, userID, title, body string, folderID, captureID, dailyDate *string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO diary_notes (user_id, title, body, folder_id, capture_id, daily_date)
		VALUES ($1, $2, $3, $4, $5, $6::date)
		RETURNING id::text`, userID, title, body, folderID, captureID, dailyDate).Scan(&id)
	return id, err
}

func addTags(ctx context.Context, tx pgx.Tx, userID, noteID string, tags []string) error {
	if len(tags) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO diary_tags (user_id, name) SELECT $1, unnest($2::text[])
		ON CONFLICT (user_id, name) DO NOTHING`, userID, tags); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO diary_note_tags (note_id, tag_id)
		SELECT $1, id FROM diary_tags WHERE user_id = $3 AND name = ANY($2::text[])
		ON CONFLICT DO NOTHING`, noteID, tags, userID)
	return err
}

// addLinks ignores self-links and ids of notes that no longer exist or belong to
// another account.
func addLinks(ctx context.Context, tx pgx.Tx, userID, noteID string, others []string) error {
	if len(others) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO diary_note_links (note_a, note_b)
		SELECT LEAST($1::uuid, n.id), GREATEST($1::uuid, n.id)
		FROM diary_notes n
		WHERE n.id = ANY($2::uuid[]) AND n.id <> $1::uuid AND n.user_id = $3
		ON CONFLICT DO NOTHING`, noteID, others, userID)
	return err
}

func requireRow(affected int64, err error) error {
	if err != nil {
		return err
	}
	if affected == 0 {
		return errNotFound
	}
	return nil
}
