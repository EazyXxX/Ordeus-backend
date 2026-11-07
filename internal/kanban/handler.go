// Package kanban implements the task board: columns, cards and their order.
package kanban

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
)

const (
	maxTitleLength       = 200
	maxDescriptionLength = 10_000
	maxCategoryLength    = 50
	maxChecklistItems    = 100
)

var (
	// Colors lists the palette shared by every colored item in the app.
	Colors     = []string{"gray", "red", "orange", "yellow", "green", "blue", "purple", "pink"}
	priorities = []string{"none", "low", "medium", "high", "urgent"}
)

type handler struct {
	store *store
}

// Register adds the board routes.
func (s *Service) Register(mux *http.ServeMux) {
	h := &handler{store: s.store}

	mux.HandleFunc("GET /api/kanban/captures", s.listCaptures)
	mux.HandleFunc("POST /api/kanban/captures", s.createCapture)
	mux.HandleFunc("POST /api/kanban/captures/{id}/retry", s.retryCapture)
	mux.HandleFunc("DELETE /api/kanban/captures/{id}", s.deleteCapture)
	mux.HandleFunc("GET /api/kanban/board", h.getBoard)
	mux.HandleFunc("POST /api/kanban/columns", h.createColumn)
	mux.HandleFunc("PATCH /api/kanban/columns/{id}", h.updateColumn)
	mux.HandleFunc("DELETE /api/kanban/columns/{id}", h.deleteColumn)
	mux.HandleFunc("POST /api/kanban/columns/{id}/move", h.moveColumn)
	mux.HandleFunc("POST /api/kanban/cards", h.createCard)
	mux.HandleFunc("PATCH /api/kanban/cards/{id}", h.updateCard)
	mux.HandleFunc("DELETE /api/kanban/cards/{id}", h.deleteCard)
	mux.HandleFunc("POST /api/kanban/cards/{id}/move", h.moveCard)
}

func (h *handler) getBoard(w http.ResponseWriter, r *http.Request) {
	board, err := h.store.board(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, board)
}

func (h *handler) createColumn(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title string `json:"title"`
	}
	if !httpjson.Bind(w, r, &input) || !validTitle(w, &input.Title) {
		return
	}
	column, err := h.store.createColumn(r.Context(), auth.UserID(r.Context()), input.Title)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusCreated, column)
}

func (h *handler) updateColumn(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Title *string `json:"title"`
		Color *string `json:"color"`
	}
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) {
		return
	}
	if input.Title != nil && !validTitle(w, input.Title) {
		return
	}
	if input.Color != nil && !slices.Contains(Colors, *input.Color) {
		httpjson.Error(w, http.StatusBadRequest, "unknown color")
		return
	}
	finish(w, r, h.store.updateColumn(r.Context(), auth.UserID(r.Context()), id, input.Title, input.Color))
}

func (h *handler) deleteColumn(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, h.store.deleteColumn(r.Context(), auth.UserID(r.Context()), id))
	}
}

func (h *handler) moveColumn(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Index int `json:"index"`
	}
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) {
		return
	}
	finish(w, r, h.store.moveColumn(r.Context(), auth.UserID(r.Context()), id, input.Index))
}

func (h *handler) createCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ColumnID string `json:"columnId"`
		Title    string `json:"title"`
	}
	if !httpjson.Bind(w, r, &input) || !validID(w, input.ColumnID) || !validTitle(w, &input.Title) {
		return
	}
	card, err := h.store.createCard(r.Context(), auth.UserID(r.Context()), input.ColumnID, input.Title)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusCreated, card)
}

func (h *handler) updateCard(w http.ResponseWriter, r *http.Request) {
	var input CardInput
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) || !validCard(w, &input) {
		return
	}
	finish(w, r, h.store.updateCard(r.Context(), auth.UserID(r.Context()), id, input))
}

func (h *handler) deleteCard(w http.ResponseWriter, r *http.Request) {
	if id, ok := httpjson.PathID(w, r); ok {
		finish(w, r, h.store.deleteCard(r.Context(), auth.UserID(r.Context()), id))
	}
}

func (h *handler) moveCard(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ColumnID string `json:"columnId"`
		Index    int    `json:"index"`
	}
	id, ok := httpjson.PathID(w, r)
	if !ok || !httpjson.Bind(w, r, &input) || !validID(w, input.ColumnID) {
		return
	}
	finish(w, r, h.store.moveCard(r.Context(), auth.UserID(r.Context()), id, input.ColumnID, input.Index))
}

func validID(w http.ResponseWriter, id string) bool {
	if uuid.Validate(id) != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid id")
		return false
	}
	return true
}

func validTitle(w http.ResponseWriter, title *string) bool {
	*title = strings.TrimSpace(*title)
	if *title == "" || utf8.RuneCountInString(*title) > maxTitleLength {
		httpjson.Error(w, http.StatusBadRequest, "title must be 1 to 200 characters long")
		return false
	}
	return true
}

// validCard reports the first problem and normalizes the input.
func validCard(w http.ResponseWriter, input *CardInput) bool {
	if !validTitle(w, &input.Title) {
		return false
	}
	if problem := cardProblem(input); problem != "" {
		httpjson.Error(w, http.StatusBadRequest, problem)
		return false
	}
	return true
}

func cardProblem(input *CardInput) string {
	input.Checklist = cleanChecklist(input.Checklist)
	input.Category = strings.TrimSpace(input.Category)

	switch {
	case utf8.RuneCountInString(input.Description) > maxDescriptionLength:
		return "description is too long"
	case utf8.RuneCountInString(input.Category) > maxCategoryLength:
		return "category is too long"
	case !slices.Contains(Colors, input.Color):
		return "unknown color"
	case !slices.Contains(priorities, input.Priority):
		return "unknown priority"
	case !validDate(input.DueDate):
		return "invalid due date"
	case len(input.Checklist) > maxChecklistItems:
		return "checklist is too long"
	case slices.ContainsFunc(input.Checklist, func(item ChecklistItem) bool {
		return utf8.RuneCountInString(item.Title) > maxTitleLength
	}):
		return "checklist item is too long"
	}
	return ""
}

func validDate(date *string) bool {
	if date == nil {
		return true
	}
	_, err := time.Parse(time.DateOnly, *date)
	return err == nil
}

// cleanChecklist trims titles and drops empty items. The result is never nil,
// because the column stores a JSON array.
func cleanChecklist(items []ChecklistItem) []ChecklistItem {
	result := make([]ChecklistItem, 0, len(items))
	for _, item := range items {
		item.Title = strings.TrimSpace(item.Title)
		if item.Title != "" {
			result = append(result, item)
		}
	}
	return result
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
	slog.Error("kanban request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	httpjson.Error(w, http.StatusInternalServerError, "internal error")
}
