package ai

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ordeus/backend/internal/httpjson"
)

const (
	modelSettingKey  = "ai.model"
	listModelTimeout = 3 * time.Second

	// Ten minutes of 16 kHz mono audio, the longest recording the app makes.
	maxAudioBytes = 10*60*bytesPerSecond + wavHeaderSize
)

// RestoreModel applies the model chosen in the app, if any.
func RestoreModel(ctx context.Context, pool *pgxpool.Pool, client *Client) error {
	var model string
	err := pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, modelSettingKey).Scan(&model)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	client.SetModel(model)
	return nil
}

// Register adds the model selection routes.
func Register(mux *http.ServeMux, pool *pgxpool.Pool, client *Client) {
	mux.HandleFunc("GET /api/ai/models", func(w http.ResponseWriter, r *http.Request) {
		models, available := listModels(r.Context(), client)
		httpjson.Write(w, http.StatusOK, map[string]any{
			"current":   client.Model(),
			"models":    models,
			"available": available,
		})
	})

	mux.HandleFunc("POST /api/ai/transcribe", func(w http.ResponseWriter, r *http.Request) {
		transcribe(w, r, client)
	})

	mux.HandleFunc("PUT /api/ai/model", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if !httpjson.Bind(w, r, &input) {
			return
		}
		input.Model = strings.TrimSpace(input.Model)

		models, available := listModels(r.Context(), client)
		if !available {
			httpjson.Error(w, http.StatusServiceUnavailable, "model provider is unavailable")
			return
		}
		if !slices.Contains(models, input.Model) {
			httpjson.Error(w, http.StatusBadRequest, "unknown model")
			return
		}

		_, err := pool.Exec(r.Context(), `
			INSERT INTO app_settings (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, modelSettingKey, input.Model)
		if err != nil {
			slog.Error("save model", "error", err)
			httpjson.Error(w, http.StatusInternalServerError, "internal error")
			return
		}
		client.SetModel(input.Model)
		w.WriteHeader(http.StatusNoContent)
	})
}

func transcribe(w http.ResponseWriter, r *http.Request, client *Client) {
	wav, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAudioBytes))
	if err != nil {
		httpjson.Error(w, http.StatusRequestEntityTooLarge, "recording is too long")
		return
	}

	text, err := client.Transcribe(r.Context(), wav)
	switch {
	case errors.Is(err, errInvalidAudio):
		httpjson.Error(w, http.StatusBadRequest, err.Error())
	case err != nil:
		slog.Warn("transcribe", "model", client.Model(), "error", err)
		httpjson.Error(w, http.StatusBadGateway, "model could not transcribe the recording")
	default:
		httpjson.Write(w, http.StatusOK, map[string]string{"text": text})
	}
}

// listModels never fails the request: a stopped provider is a state the
// interface shows, so it answers with an empty list and available = false.
func listModels(ctx context.Context, client *Client) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, listModelTimeout)
	defer cancel()

	models, err := client.Models(ctx)
	if err != nil {
		slog.Warn("list models", "error", err)
		return []string{}, false
	}
	return models, true
}
