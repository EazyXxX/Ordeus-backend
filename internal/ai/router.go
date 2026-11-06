package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
)

const (
	providerLocal  = "local"
	providerRemote = "remote"

	localListTimeout  = 3 * time.Second
	remoteListTimeout = 10 * time.Second

	maxModelNameLength = 200
	maxAPIKeyLength    = 1000
	keyHintLength      = 4

	// Ten minutes of 16 kHz mono audio, the longest recording the app makes.
	maxAudioBytes = 10*60*bytesPerSecond + wavHeaderSize
)

type RouterConfig struct {
	LocalBaseURL         string
	LocalAPIKey          string
	LocalModel           string
	LocalReasoningEffort string
	SecretFile           string
	AllowPrivateURLs     bool
}

// Router builds the model client each account chose.
type Router struct {
	pool       *pgxpool.Pool
	config     RouterConfig
	box        *secretBox
	remoteHTTP *http.Client
}

func NewRouter(pool *pgxpool.Pool, config RouterConfig) (*Router, error) {
	box, err := loadSecretBox(config.SecretFile)
	if err != nil {
		return nil, err
	}
	return &Router{
		pool:       pool,
		config:     config,
		box:        box,
		remoteHTTP: newRemoteHTTPClient(config.AllowPrivateURLs),
	}, nil
}

type settings struct {
	provider  string
	local     string
	baseURL   string
	model     string
	sealedKey []byte
	keyHint   string
}

func (r *Router) load(ctx context.Context, userID string) (settings, error) {
	loaded := settings{provider: providerLocal}
	err := r.pool.QueryRow(ctx, `
		SELECT provider, local_model, remote_base_url, remote_model, remote_api_key, remote_key_hint
		FROM ai_settings WHERE user_id = $1`, userID,
	).Scan(&loaded.provider, &loaded.local, &loaded.baseURL, &loaded.model, &loaded.sealedKey, &loaded.keyHint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return settings{}, err
	}
	if loaded.local == "" {
		loaded.local = r.config.LocalModel
	}
	return loaded, nil
}

func (r *Router) save(ctx context.Context, userID string, values settings) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO ai_settings (user_id, provider, local_model, remote_base_url, remote_model, remote_api_key, remote_key_hint)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
			provider = EXCLUDED.provider,
			local_model = EXCLUDED.local_model,
			remote_base_url = EXCLUDED.remote_base_url,
			remote_model = EXCLUDED.remote_model,
			remote_api_key = EXCLUDED.remote_api_key,
			remote_key_hint = EXCLUDED.remote_key_hint`,
		userID, values.provider, values.local, values.baseURL, values.model, values.sealedKey, values.keyHint)
	return err
}

func (r *Router) localClient(model string) *Client {
	return New(r.config.LocalBaseURL, r.config.LocalAPIKey, model, r.config.LocalReasoningEffort)
}

func (r *Router) remoteClient(baseURL, apiKey, model string) *Client {
	client := New(baseURL, apiKey, model, "")
	client.http = r.remoteHTTP
	return client
}

func (r *Router) savedKey(values settings, userID string) (string, error) {
	if values.sealedKey == nil {
		return "", nil
	}
	return r.box.open(values.sealedKey, userID)
}

// ForUser returns the model that sorts this account's entries.
func (r *Router) ForUser(ctx context.Context, userID string) (*Client, error) {
	values, err := r.load(ctx, userID)
	if err != nil {
		return nil, err
	}
	if values.provider != providerRemote {
		return r.localClient(values.local), nil
	}
	key, err := r.savedKey(values, userID)
	if err != nil {
		return nil, err
	}
	return r.remoteClient(values.baseURL, key, values.model), nil
}

// Register adds the model settings routes.
func (r *Router) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/settings", r.getSettings)
	mux.HandleFunc("PATCH /api/ai/settings", r.updateSettings)
	mux.HandleFunc("POST /api/ai/remote/models", r.listRemoteModels)
	mux.HandleFunc("DELETE /api/ai/remote", r.deleteRemote)
	mux.HandleFunc("POST /api/ai/transcribe", r.transcribe)
}

type remoteView struct {
	BaseURL   string `json:"baseUrl"`
	Model     string `json:"model"`
	HasAPIKey bool   `json:"hasApiKey"`
	KeyHint   string `json:"keyHint"`
}

type localView struct {
	Model     string   `json:"model"`
	Models    []string `json:"models"`
	Available bool     `json:"available"`
}

type settingsView struct {
	Provider string     `json:"provider"`
	Local    localView  `json:"local"`
	Remote   remoteView `json:"remote"`
}

func (r *Router) localModels(ctx context.Context) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, localListTimeout)
	defer cancel()

	models, err := r.localClient("").Models(ctx)
	if err != nil {
		slog.Warn("list local models", "error", err)
		return []string{}, false
	}
	return models, true
}

func (r *Router) getSettings(w http.ResponseWriter, request *http.Request) {
	values, err := r.load(request.Context(), auth.UserID(request.Context()))
	if err != nil {
		fail(w, request, err)
		return
	}
	r.writeSettings(w, request, values)
}

func (r *Router) writeSettings(w http.ResponseWriter, request *http.Request, values settings) {
	models, available := r.localModels(request.Context())
	httpjson.Write(w, http.StatusOK, settingsView{
		Provider: values.provider,
		Local:    localView{Model: values.local, Models: models, Available: available},
		Remote: remoteView{
			BaseURL:   values.baseURL,
			Model:     values.model,
			HasAPIKey: values.sealedKey != nil,
			KeyHint:   values.keyHint,
		},
	})
}

type remoteInput struct {
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
}

func (r *Router) updateSettings(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Provider   *string      `json:"provider"`
		LocalModel *string      `json:"localModel"`
		Remote     *remoteInput `json:"remote"`
	}
	if !httpjson.Bind(w, request, &input) {
		return
	}

	userID := auth.UserID(request.Context())
	values, err := r.load(request.Context(), userID)
	if err != nil {
		fail(w, request, err)
		return
	}

	if input.LocalModel != nil {
		models, available := r.localModels(request.Context())
		if !available {
			httpjson.Error(w, http.StatusServiceUnavailable, "local models are unavailable")
			return
		}
		if !slices.Contains(models, *input.LocalModel) {
			httpjson.Error(w, http.StatusBadRequest, "unknown model")
			return
		}
		values.local = *input.LocalModel
	}
	if input.Remote != nil {
		if values, err = r.applyRemote(values, userID, *input.Remote); err != nil {
			httpjson.Error(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if input.Provider != nil {
		values.provider = *input.Provider
	}

	switch {
	case values.provider != providerLocal && values.provider != providerRemote:
		httpjson.Error(w, http.StatusBadRequest, "unknown provider")
		return
	case values.provider == providerRemote && (values.baseURL == "" || values.model == ""):
		httpjson.Error(w, http.StatusBadRequest, "remote model is not configured")
		return
	}

	if err := r.save(request.Context(), userID, values); err != nil {
		fail(w, request, err)
		return
	}
	r.writeSettings(w, request, values)
}

func (r *Router) applyRemote(values settings, userID string, input remoteInput) (settings, error) {
	baseURL, err := normalizeRemoteURL(input.BaseURL, r.config.AllowPrivateURLs)
	if err != nil {
		return values, err
	}
	model := strings.TrimSpace(input.Model)
	if model == "" || utf8.RuneCountInString(model) > maxModelNameLength {
		return values, errors.New("model name must be 1 to 200 characters long")
	}
	key := strings.TrimSpace(input.APIKey)
	if len(key) > maxAPIKeyLength {
		return values, errors.New("api key is too long")
	}

	// A saved key is never sent to a new address: otherwise a hijacked session
	// could change the address and collect the key there.
	if baseURL != values.baseURL {
		values.sealedKey = nil
		values.keyHint = ""
	}
	if key != "" {
		if values.sealedKey, err = r.box.seal(key, userID); err != nil {
			return values, fmt.Errorf("encrypt api key: %w", err)
		}
		values.keyHint = key[max(len(key)-keyHintLength, 0):]
	}
	values.baseURL = baseURL
	values.model = model
	return values, nil
}

// listRemoteModels doubles as a connection check before anything is saved.
func (r *Router) listRemoteModels(w http.ResponseWriter, request *http.Request) {
	var input struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
	}
	if !httpjson.Bind(w, request, &input) {
		return
	}
	baseURL, err := normalizeRemoteURL(input.BaseURL, r.config.AllowPrivateURLs)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	key := strings.TrimSpace(input.APIKey)
	if key == "" {
		userID := auth.UserID(request.Context())
		values, err := r.load(request.Context(), userID)
		if err != nil {
			fail(w, request, err)
			return
		}
		// The saved key only ever goes to the address it was saved for.
		if baseURL == values.baseURL {
			if key, err = r.savedKey(values, userID); err != nil {
				httpjson.Error(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}

	ctx, cancel := context.WithTimeout(request.Context(), remoteListTimeout)
	defer cancel()
	models, err := r.remoteClient(baseURL, key, "").Models(ctx)
	if err != nil {
		slog.Warn("list remote models", "error", err)
		httpjson.Error(w, http.StatusBadGateway, "could not reach the api")
		return
	}
	httpjson.Write(w, http.StatusOK, map[string]any{"models": models})
}

func (r *Router) deleteRemote(w http.ResponseWriter, request *http.Request) {
	userID := auth.UserID(request.Context())
	values, err := r.load(request.Context(), userID)
	if err != nil {
		fail(w, request, err)
		return
	}
	values.provider = providerLocal
	values.baseURL, values.model, values.keyHint, values.sealedKey = "", "", "", nil
	if err := r.save(request.Context(), userID, values); err != nil {
		fail(w, request, err)
		return
	}
	r.writeSettings(w, request, values)
}

func fail(w http.ResponseWriter, request *http.Request, err error) {
	slog.Error("ai request failed", "method", request.Method, "path", request.URL.Path, "error", err)
	httpjson.Error(w, http.StatusInternalServerError, "internal error")
}
