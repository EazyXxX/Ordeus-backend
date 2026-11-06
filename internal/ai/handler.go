package ai

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"ordeus/backend/internal/auth"
	"ordeus/backend/internal/httpjson"
)

// transcribe always uses the local model, because recorded voice is more
// personal than text and few vendors accept audio in chat requests.
func (r *Router) transcribe(w http.ResponseWriter, request *http.Request) {
	wav, err := io.ReadAll(http.MaxBytesReader(w, request.Body, maxAudioBytes))
	if err != nil {
		httpjson.Error(w, http.StatusRequestEntityTooLarge, "recording is too long")
		return
	}

	values, err := r.load(request.Context(), auth.UserID(request.Context()))
	if err != nil {
		fail(w, request, err)
		return
	}

	text, err := r.localClient(values.local).Transcribe(request.Context(), wav)
	switch {
	case errors.Is(err, errInvalidAudio):
		httpjson.Error(w, http.StatusBadRequest, err.Error())
	case err != nil:
		slog.Warn("transcribe", "model", values.local, "error", err)
		httpjson.Error(w, http.StatusBadGateway, "model could not transcribe the recording")
	default:
		httpjson.Write(w, http.StatusOK, map[string]string{"text": text})
	}
}
