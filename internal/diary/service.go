// Package diary implements the journal: notes with folders, tags and links,
// plus zen-mode captures that an AI model sorts into notes in the background.
package diary

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	statusPending = "pending"

	// A local model can take minutes on a long entry.
	organizeTimeout = 5 * time.Minute
	idlePollPeriod  = 30 * time.Second
)

// ModelFor returns the model an account chose for sorting its entries.
type ModelFor func(ctx context.Context, userID string) (StructuredModel, error)

type Service struct {
	store    *store
	files    imageFiles
	modelFor ModelFor
	wake     chan struct{}
}

// New keeps note images under uploadsDir.
func New(pool *pgxpool.Pool, modelFor ModelFor, uploadsDir string) *Service {
	return &Service{
		store:    &store{pool: pool},
		files:    imageFiles{dir: uploadsDir},
		modelFor: modelFor,
		wake:     make(chan struct{}, 1),
	}
}

// Run sorts captures and writes summaries one at a time until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	if err := s.store.requeueInterrupted(ctx); err != nil {
		slog.Error("requeue captures", "error", err)
	}
	if err := s.store.requeueInterruptedSummaries(ctx); err != nil {
		slog.Error("requeue summaries", "error", err)
	}
	if err := s.store.requeueInterruptedDays(ctx); err != nil {
		slog.Error("requeue days", "error", err)
	}
	for {
		// New entries go first, because their author is waiting for them.
		err := s.processNext(ctx)
		if errors.Is(err, errNotFound) {
			err = s.processNextDay(ctx)
		}
		if errors.Is(err, errNotFound) {
			err = s.processNextSummary(ctx)
		}
		if err == nil {
			continue
		}
		if !errors.Is(err, errNotFound) && ctx.Err() == nil {
			slog.Error("process capture", "error", err)
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
	known, err := s.store.organizerContext(ctx, capture.userID)
	if err != nil {
		return s.fail(ctx, capture.id, err)
	}
	model, err := s.modelFor(ctx, capture.userID)
	if err != nil {
		return s.fail(ctx, capture.id, err)
	}

	stored, err := s.store.captureImages(ctx, capture.id)
	if err != nil {
		return s.fail(ctx, capture.id, err)
	}
	images := s.files.modelImages(capture.userID, stored)

	organizeCtx, cancel := context.WithTimeout(ctx, organizeTimeout)
	defer cancel()
	result, err := organize(organizeCtx, model, capture.text, capture.writtenOn, known, images, len(stored))
	// A model that cannot see pictures refuses them. With text there is still
	// enough to make the note, and the pictures go into it all the same.
	if err != nil && len(images) > 0 && strings.TrimSpace(capture.text) != "" && organizeCtx.Err() == nil {
		slog.Warn("organize with images failed, trying text alone", "id", capture.id, "error", err)
		result, err = organize(organizeCtx, model, capture.text, capture.writtenOn, known, nil, len(stored))
	}
	if err != nil {
		return s.fail(ctx, capture.id, err)
	}
	if err := s.store.applyPlan(ctx, capture.userID, capture.id, result, known.Notes); err != nil {
		return s.fail(ctx, capture.id, err)
	}
	slog.Info("capture organized", "id", capture.id, "notes", len(result.Notes))
	return nil
}

// fail records why a capture stopped so it can be retried. During shutdown the
// capture stays as it is and returns to the queue on the next start.
func (s *Service) fail(ctx context.Context, id string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	slog.Warn("capture failed", "id", id, "error", cause)
	return s.store.failCapture(ctx, id, cause.Error())
}
