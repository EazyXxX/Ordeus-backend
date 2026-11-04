// Package httpjson holds the JSON request and response helpers for handlers.
package httpjson

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20

// Bind decodes the request body and answers 400 itself when it is invalid.
func Bind(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// PathID returns the {id} path value and answers 404 itself when it is not a UUID.
func PathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if uuid.Validate(id) != nil {
		Error(w, http.StatusNotFound, "not found")
		return "", false
	}
	return id, true
}

func Write(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "error", err)
	}
}

func Error(w http.ResponseWriter, status int, message string) {
	Write(w, status, map[string]string{"error": message})
}
