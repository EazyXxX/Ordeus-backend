// Package calendar serves dated notes and the kanban tasks that have due dates.
package calendar

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
	"ordeus/backend/internal/kanban"
)

const (
	maxTitleLength = 200
	maxBodyLength  = 10_000
	maxRangeDays   = 400
	timeLayout     = "15:04"
)

type handler struct {
	store *store
}

func Register(mux *http.ServeMux, pool *pgxpool.Pool) {
	h := &handler{store: &store{pool: pool}}

	mux.HandleFunc("GET /api/calendar", h.getRange)
	mux.HandleFunc("POST /api/calendar/notes", h.createNote)
	mux.HandleFunc("PATCH /api/calendar/notes/{id}", h.updateNote)
	mux.HandleFunc("DELETE /api/calendar/notes/{id}", h.deleteNote)
}

func (h *handler) getRange(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if problem := rangeProblem(from, to); problem != "" {
		httpjson.Error(w, http.StatusBadRequest, problem)
		return
	}
	result, err := h.store.inRange(r.Context(), auth.UserID(r.Context()), from, to)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, result)
}

func (h *handler) createNote(w http.ResponseWriter, r *http.Request) {
	var input NoteInput
	if !httpjson.Bind(w, r, &input) || !validNote(w, &input) {
		return
	}
	id, err := h.store.createNote(r.Context(), auth.UserID(r.Context()), input)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *handler) updateNote(w http.ResponseWriter, r *http.Request) {
	var input NoteInput
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) || !validNote(w, &input) {
		return
	}
	finish(w, r, h.store.updateNote(r.Context(), auth.UserID(r.Context()), id, input))
}

func (h *handler) deleteNote(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, h.store.deleteNote(r.Context(), auth.UserID(r.Context()), id))
	}
}

func rangeProblem(from, to string) string {
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		return "invalid from date"
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return "invalid to date"
	}
	if end.Before(start) {
		return "to must not be before from"
	}
	if end.Sub(start) > maxRangeDays*24*time.Hour {
		return "range is too long"
	}
	return ""
}

func validNote(w http.ResponseWriter, input *NoteInput) bool {
	if problem := noteProblem(input); problem != "" {
		httpjson.Error(w, http.StatusBadRequest, problem)
		return false
	}
	return true
}

func noteProblem(input *NoteInput) string {
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)

	if _, err := time.Parse(time.DateOnly, input.Date); err != nil {
		return "invalid date"
	}
	if input.Time != nil {
		if _, err := time.Parse(timeLayout, *input.Time); err != nil {
			return "invalid time"
		}
	}
	switch {
	case input.Title == "" || utf8.RuneCountInString(input.Title) > maxTitleLength:
		return "title must be 1 to 200 characters long"
	case utf8.RuneCountInString(input.Body) > maxBodyLength:
		return "body is too long"
	case !slices.Contains(kanban.Colors, input.Color):
		return "unknown color"
	}
	return ""
}

func finish(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errNotFound) {
		httpjson.Error(w, http.StatusNotFound, "not found")
		return
	}
	slog.Error("calendar request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	httpjson.Error(w, http.StatusInternalServerError, "internal error")
}
