package kanban

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
)

const (
	maxCaptureText = 50_000
	// A local model can take minutes on a long entry.
	organizeTimeout = 5 * time.Minute
	idlePollPeriod  = 30 * time.Second
)

var errNoColumns = errors.New("the board has no columns")

// ModelFor returns the model an account chose.
type ModelFor func(ctx context.Context, userID string) (StructuredModel, error)

// Service owns the board routes and the worker that turns zen-mode text into cards.
type Service struct {
	store    *store
	modelFor ModelFor
	wake     chan struct{}
}

func New(pool *pgxpool.Pool, modelFor ModelFor) *Service {
	return &Service{
		store:    &store{pool: pool},
		modelFor: modelFor,
		wake:     make(chan struct{}, 1),
	}
}

type Capture struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	CreatedAt time.Time `json:"createdAt"`
}

// Run turns captures into cards one at a time until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	if err := s.store.requeueInterrupted(ctx); err != nil {
		slog.Error("requeue task captures", "error", err)
	}
	for {
		err := s.processNext(ctx)
		if err == nil {
			continue
		}
		if !errors.Is(err, errNotFound) && ctx.Err() == nil {
			slog.Error("process task capture", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(idlePollPeriod):
		}
	}
}

func (s *Service) notifyWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) processNext(ctx context.Context) error {
	capture, err := s.store.claimCapture(ctx)
	if err != nil {
		return err
	}

	model, err := s.modelFor(ctx, capture.userID)
	if err != nil {
		return s.failCapture(ctx, capture.id, err)
	}
	categories, err := s.store.categories(ctx, capture.userID)
	if err != nil {
		return s.failCapture(ctx, capture.id, err)
	}
	organizeCtx, cancel := context.WithTimeout(ctx, organizeTimeout)
	defer cancel()
	tasks, err := organize(organizeCtx, model, capture.text, capture.writtenOn, categories)
	if err != nil {
		return s.failCapture(ctx, capture.id, err)
	}
	if err := s.store.applyTasks(ctx, capture.userID, capture.id, tasks); err != nil {
		return s.failCapture(ctx, capture.id, err)
	}
	slog.Info("task capture organized", "id", capture.id, "tasks", len(tasks))
	return nil
}

// failCapture records why a capture stopped so it can be retried. During
// shutdown the capture returns to the queue on the next start instead.
func (s *Service) failCapture(ctx context.Context, id string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	slog.Warn("task capture failed", "id", id, "error", cause)
	return s.store.markCaptureFailed(ctx, id, cause.Error())
}

func (s *Service) listCaptures(w http.ResponseWriter, r *http.Request) {
	captures, err := s.store.unfinishedCaptures(r.Context(), auth.UserID(r.Context()))
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, captures)
}

func (s *Service) createCapture(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text  string `json:"text"`
		Today string `json:"today"`
	}
	if !httpjson.Bind(w, r, &input) {
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" || utf8.RuneCountInString(input.Text) > maxCaptureText {
		httpjson.Error(w, http.StatusBadRequest, "text must be 1 to 50000 characters long")
		return
	}
	if _, err := time.Parse(time.DateOnly, input.Today); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid date")
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

type claimedCapture struct {
	id, userID, text, writtenOn string
}

func (s *store) createCapture(ctx context.Context, userID, text, writtenOn string) (Capture, error) {
	capture := Capture{Text: text, Status: "pending"}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO task_captures (user_id, raw_text, written_on) VALUES ($1, $2, $3::date)
		RETURNING id::text, created_at`, userID, text, writtenOn).Scan(&capture.ID, &capture.CreatedAt)
	return capture, err
}

func (s *store) unfinishedCaptures(ctx context.Context, userID string) ([]Capture, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, raw_text, status, error, created_at
		FROM task_captures
		WHERE status <> 'done' AND user_id = $1
		ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Capture])
}

func (s *store) retryCapture(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE task_captures SET status = 'pending', error = ''
		WHERE id = $1 AND user_id = $2 AND status = 'failed'`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

func (s *store) deleteFailedCapture(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM task_captures WHERE id = $1 AND user_id = $2 AND status = 'failed'`, id, userID)
	return requireRow(tag.RowsAffected(), err)
}

// requeueInterrupted returns captures left mid-flight by a stopped server.
func (s *store) requeueInterrupted(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE task_captures SET status = 'pending' WHERE status = 'processing'`)
	return err
}

func (s *store) claimCapture(ctx context.Context) (claimedCapture, error) {
	var capture claimedCapture
	err := s.pool.QueryRow(ctx, `
		UPDATE task_captures SET status = 'processing'
		WHERE id = (
			SELECT id FROM task_captures WHERE status = 'pending'
			ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, user_id::text, raw_text, to_char(written_on, 'YYYY-MM-DD')`,
	).Scan(&capture.id, &capture.userID, &capture.text, &capture.writtenOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return claimedCapture{}, errNotFound
	}
	return capture, err
}

func (s *store) markCaptureFailed(ctx context.Context, id, message string) error {
	_, err := s.pool.Exec(ctx, `UPDATE task_captures SET status = 'failed', error = $2 WHERE id = $1`, id, message)
	return err
}

// applyTasks adds the cards to the end of the account's first column and
// finishes the capture in one step, so a retry can never duplicate cards.
func (s *store) applyTasks(ctx context.Context, userID, captureID string, tasks []plannedTask) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var columnID string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM kanban_columns WHERE user_id = $1
			ORDER BY position, id LIMIT 1 FOR UPDATE`, userID).Scan(&columnID)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoColumns
		}
		if err != nil {
			return err
		}

		var position int
		err = tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(position) + 1, 0) FROM kanban_cards WHERE column_id = $1`, columnID).Scan(&position)
		if err != nil {
			return err
		}
		for i, task := range tasks {
			_, err := tx.Exec(ctx, `
				INSERT INTO kanban_cards (user_id, column_id, title, description, category, priority, due_date, position)
				VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, '')::date, $8)`,
				userID, columnID, task.Title, task.Description, task.Category, task.Priority, task.DueDate, position+i)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx,
			`UPDATE task_captures SET status = 'done', error = '', processed_at = now() WHERE id = $1`, captureID)
		return err
	})
}
