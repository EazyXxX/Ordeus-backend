package diary

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
	"ordeus/backend/internal/kanban"
)

const (
	maxTitleLength = 200
	maxBodyLength  = 50_000
	maxCaptureText = 50_000
)

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/diary/notes", s.listNotes)
	mux.HandleFunc("POST /api/diary/notes", s.createNote)
	mux.HandleFunc("PATCH /api/diary/notes/{id}", s.updateNote)
	mux.HandleFunc("DELETE /api/diary/notes/{id}", s.deleteNote)

	mux.HandleFunc("GET /api/diary/folders", s.listFolders)
	mux.HandleFunc("PATCH /api/diary/folders/{id}", s.updateFolder)
	mux.HandleFunc("DELETE /api/diary/folders/{id}", s.deleteFolder)

	mux.HandleFunc("GET /api/diary/tags", s.listTags)
	mux.HandleFunc("PATCH /api/diary/tags/{id}", s.updateTag)
	mux.HandleFunc("DELETE /api/diary/tags/{id}", s.deleteTag)

	mux.HandleFunc("GET /api/diary/captures", s.listCaptures)
	mux.HandleFunc("POST /api/diary/captures", s.createCapture)
	mux.HandleFunc("POST /api/diary/captures/{id}/retry", s.retryCapture)
	mux.HandleFunc("DELETE /api/diary/captures/{id}", s.deleteCapture)
}

func (s *Service) listNotes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := NoteFilter{
		Daily:    query.Get("daily") == "true",
		FolderID: query.Get("folder"),
		Tag:      query.Get("tag"),
		Query:    strings.TrimSpace(query.Get("q")),
		From:     query.Get("from"),
		To:       query.Get("to"),
	}
	if filter.FolderID != "" && uuid.Validate(filter.FolderID) != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid folder")
		return
	}
	if (filter.From != "" && !validDate(filter.From)) || (filter.To != "" && !validDate(filter.To)) {
		httpjson.Error(w, http.StatusBadRequest, "invalid date")
		return
	}
	notes, err := s.store.notes(r.Context(), auth.UserID(r.Context()), filter)
	respond(w, r, notes, err)
}

func (s *Service) createNote(w http.ResponseWriter, r *http.Request) {
	var input NoteInput
	if !httpjson.Bind(w, r, &input) || !validNote(w, &input) {
		return
	}
	id, err := s.store.createNote(r.Context(), auth.UserID(r.Context()), input)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Service) updateNote(w http.ResponseWriter, r *http.Request) {
	var input NoteInput
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) || !validNote(w, &input) {
		return
	}
	finish(w, r, s.store.updateNote(r.Context(), auth.UserID(r.Context()), id, input))
}

func (s *Service) deleteNote(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, s.store.deleteNote(r.Context(), auth.UserID(r.Context()), id))
	}
}

func (s *Service) listFolders(w http.ResponseWriter, r *http.Request) {
	folders, err := s.store.folders(r.Context(), auth.UserID(r.Context()))
	respond(w, r, folders, err)
}

func (s *Service) updateFolder(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
		Emoji *string `json:"emoji"`
	}
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) {
		return
	}
	if input.Name != nil {
		*input.Name = strings.TrimSpace(*input.Name)
		if *input.Name == "" || utf8.RuneCountInString(*input.Name) > maxTitleLength {
			httpjson.Error(w, http.StatusBadRequest, "name must be 1 to 200 characters long")
			return
		}
	}
	if input.Color != nil && !validColor(w, *input.Color) {
		return
	}
	if !validEmojiChoice(w, input.Emoji) {
		return
	}
	finish(w, r, s.store.updateFolder(r.Context(), auth.UserID(r.Context()), id, input.Name, input.Color, input.Emoji))
}

func (s *Service) deleteFolder(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, s.store.deleteFolder(r.Context(), auth.UserID(r.Context()), id))
	}
}

func (s *Service) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.tags(r.Context(), auth.UserID(r.Context()))
	respond(w, r, tags, err)
}

func (s *Service) updateTag(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Color *string `json:"color"`
		Emoji *string `json:"emoji"`
	}
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) {
		return
	}
	if (input.Color != nil && !validColor(w, *input.Color)) || !validEmojiChoice(w, input.Emoji) {
		return
	}
	finish(w, r, s.store.updateTag(r.Context(), auth.UserID(r.Context()), id, input.Color, input.Emoji))
}

func (s *Service) deleteTag(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, s.store.deleteTag(r.Context(), auth.UserID(r.Context()), id))
	}
}

func (s *Service) listCaptures(w http.ResponseWriter, r *http.Request) {
	captures, err := s.store.unfinishedCaptures(r.Context(), auth.UserID(r.Context()))
	respond(w, r, captures, err)
}

func (s *Service) createCapture(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text  string `json:"text"`
		Today string `json:"today"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	if _, err := time.Parse(time.DateOnly, input.Today); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid date")
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" || utf8.RuneCountInString(input.Text) > maxCaptureText {
		httpjson.Error(w, http.StatusBadRequest, "text must be 1 to 50000 characters long")
		return
	}
	capture, err := s.store.createCapture(r.Context(), auth.UserID(r.Context()), input.Text, input.Today)
	if err != nil {
		fail(w, r, err)
		return
	}
	s.notifyWorker()
	httpjson.Write(w, http.StatusCreated, capture)
}

func (s *Service) retryCapture(w http.ResponseWriter, r *http.Request) {
	id, ok := httpjson.PathID(w, r)
	if !ok {
		return
	}
	err := s.store.retryCapture(r.Context(), auth.UserID(r.Context()), id)
	if err == nil {
		s.notifyWorker()
	}
	finish(w, r, err)
}

func (s *Service) deleteCapture(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, s.store.deleteFailedCapture(r.Context(), auth.UserID(r.Context()), id))
	}
}

func validDate(date string) bool {
	_, err := time.Parse(time.DateOnly, date)
	return err == nil
}

// validEmojiChoice allows an empty emoji, which removes the current one.
func validEmojiChoice(w http.ResponseWriter, emoji *string) bool {
	if emoji == nil || *emoji == "" || validEmoji(*emoji) {
		return true
	}
	httpjson.Error(w, http.StatusBadRequest, "invalid emoji")
	return false
}

func validColor(w http.ResponseWriter, color string) bool {
	if !slices.Contains(kanban.Colors, color) {
		httpjson.Error(w, http.StatusBadRequest, "unknown color")
		return false
	}
	return true
}

func validNote(w http.ResponseWriter, input *NoteInput) bool {
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	input.Folder = strings.TrimSpace(input.Folder)
	input.Tags = normalizeTags(input.Tags)

	switch {
	case input.Title == "" || utf8.RuneCountInString(input.Title) > maxTitleLength:
		httpjson.Error(w, http.StatusBadRequest, "title must be 1 to 200 characters long")
	case utf8.RuneCountInString(input.Body) > maxBodyLength:
		httpjson.Error(w, http.StatusBadRequest, "body is too long")
	case utf8.RuneCountInString(input.Folder) > maxTitleLength:
		httpjson.Error(w, http.StatusBadRequest, "folder name is too long")
	case input.DailyDate != nil && !validDate(*input.DailyDate):
		httpjson.Error(w, http.StatusBadRequest, "invalid daily date")
	case slices.ContainsFunc(input.LinkIDs, func(id string) bool { return uuid.Validate(id) != nil }):
		httpjson.Error(w, http.StatusBadRequest, "invalid link id")
	default:
		return true
	}
	return false
}

func respond(w http.ResponseWriter, r *http.Request, body any, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, body)
}

func finish(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errNotFound):
		httpjson.Error(w, http.StatusNotFound, "not found")
	case errors.Is(err, errConflict):
		httpjson.Error(w, http.StatusConflict, "already exists")
	default:
		slog.Error("diary request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		httpjson.Error(w, http.StatusInternalServerError, "internal error")
	}
}
