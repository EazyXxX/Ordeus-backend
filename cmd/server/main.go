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
	"ordeus/backend/internal/config"
	"ordeus/backend/internal/database"
	"ordeus/backend/internal/diary"
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

	aiClient := ai.New(cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel, cfg.AIReasoningEffort)
	if err := ai.RestoreModel(startupCtx, pool, aiClient); err != nil {
		return err
	}
	diaryService := diary.New(pool, aiClient)
	go diaryService.Run(ctx)

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.NewRouter(pool, diaryService, aiClient),
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
