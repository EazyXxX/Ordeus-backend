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
	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/calendar"
	"ordeus/backend/internal/diary"
	"ordeus/backend/internal/httpjson"
	"ordeus/backend/internal/kanban"
)

const pingTimeout = 2 * time.Second

// NewRouter keeps sign-in and health public. Everything else needs a session,
// so a new module is protected by registering on the protected mux.
func NewRouter(pool *pgxpool.Pool, authService *auth.Service, kanbanService *kanban.Service, diaryService *diary.Service, aiRouter *ai.Router) http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("GET /api/health", health(pool))
	authService.RegisterPublic(public)

	protected := http.NewServeMux()
	authService.RegisterProtected(protected)
	kanbanService.Register(protected)
	calendar.Register(protected, pool)
	diaryService.Register(protected)
	aiRouter.Register(protected)

	public.Handle("/api/", authService.Require(protected))
	return public
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
