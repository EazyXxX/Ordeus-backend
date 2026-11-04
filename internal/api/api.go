// Package api exposes the HTTP interface. Each organizer module registers its
// routes in NewRouter under /api/<module>.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ordeus/backend/internal/ai"
	"ordeus/backend/internal/calendar"
	"ordeus/backend/internal/diary"
	"ordeus/backend/internal/httpjson"
	"ordeus/backend/internal/kanban"
)

const pingTimeout = 2 * time.Second

func NewRouter(pool *pgxpool.Pool, diaryService *diary.Service, aiClient *ai.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", health(pool))
	kanban.Register(mux, pool)
	calendar.Register(mux, pool)
	diaryService.Register(mux)
	ai.Register(mux, pool, aiClient)
	return mux
}

func health(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), pingTimeout)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			slog.Error("health check failed", "error", err)
			httpjson.Write(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded", "database": "down"})
			return
		}
		httpjson.Write(w, http.StatusOK, map[string]string{"status": "ok", "database": "up"})
	}
}
