// Package diary implements the journal: notes with folders, tags and links,
// plus zen-mode captures that an AI model sorts into notes in the background.
package diary

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	statusPending = "pending"

	// A local model can take minutes on a long entry.
	organizeTimeout = 5 * time.Minute
	idlePollPeriod  = 30 * time.Second
)

type Service struct {
	store *store
	model structuredModel
	wake  chan struct{}
}

func New(pool *pgxpool.Pool, model structuredModel) *Service {
	return &Service{
		store: &store{pool: pool},
		model: model,
		wake:  make(chan struct{}, 1),
	}
}

// Run sorts captures one at a time until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	if err := s.store.requeueInterrupted(ctx); err != nil {
		slog.Error("requeue captures", "error", err)
	}
	for {
		err := s.processNext(ctx)
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
	known, err := s.store.organizerContext(ctx)
	if err != nil {
		return s.fail(ctx, capture, err)
	}

	organizeCtx, cancel := context.WithTimeout(ctx, organizeTimeout)
	defer cancel()
	result, err := organize(organizeCtx, s.model, capture.Text, known)
	if err != nil {
		return s.fail(ctx, capture, err)
	}
	if err := s.store.applyPlan(ctx, capture.ID, result, known.Notes); err != nil {
		return s.fail(ctx, capture, err)
	}
	slog.Info("capture organized", "id", capture.ID, "notes", len(result.Notes))
	return nil
}

// fail records why a capture stopped so it can be retried. During shutdown the
// capture stays as it is and returns to the queue on the next start.
func (s *Service) fail(ctx context.Context, capture Capture, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	slog.Warn("capture failed", "id", capture.ID, "error", cause)
	return s.store.failCapture(ctx, capture.ID, cause.Error())
}
