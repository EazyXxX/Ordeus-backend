package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ordeus/backend/internal/ai"
	"ordeus/backend/internal/api"
	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/config"
	"ordeus/backend/internal/database"
	"ordeus/backend/internal/diary"
	"ordeus/backend/internal/kanban"
)

const (
	startupTimeout  = 15 * time.Second
	shutdownTimeout = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	pool, err := database.Open(startupCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	aiRouter, err := ai.NewRouter(pool, ai.RouterConfig{
		LocalBaseURL:         cfg.AIBaseURL,
		LocalAPIKey:          cfg.AIAPIKey,
		LocalModel:           cfg.AIModel,
		LocalReasoningEffort: cfg.AIReasoningEffort,
		SecretFile:           cfg.SecretFile,
		AllowPrivateURLs:     cfg.AIAllowPrivateURLs,
	})
	if err != nil {
		return err
	}
	authService := auth.New(pool, cfg.SecureCookies)
	if err := authService.CleanExpired(startupCtx); err != nil {
		return err
	}
	diaryService := diary.New(pool, func(ctx context.Context, userID string) (diary.StructuredModel, error) {
		client, err := aiRouter.ForUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		return client, nil
	})
	go diaryService.Run(ctx)
	kanbanService := kanban.New(pool, func(ctx context.Context, userID string) (kanban.StructuredModel, error) {
		client, err := aiRouter.ForUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		return client, nil
	})
	go kanbanService.Run(ctx)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewRouter(pool, authService, kanbanService, diaryService, aiRouter),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errs := make(chan error, 1)
	go func() {
		slog.Info("server listening", "addr", cfg.Addr)
		errs <- server.ListenAndServe()
	}()

	select {
	case err := <-errs:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
