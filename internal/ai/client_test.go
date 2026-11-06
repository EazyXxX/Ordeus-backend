package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "test-model" || request.ReasoningEffort != "none" || request.ResponseFormat.Type != "json_schema" {
			t.Errorf("unexpected request: %+v", request)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"value\":\"ok\"}"}}]}`))
	}))
	defer server.Close()

	var result struct {
		Value string `json:"value"`
	}
	client := New(server.URL+"/v1/", "secret", "test-model", "none")
	if err := client.Structured(context.Background(), "s", "u", "n", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "ok" {
		t.Errorf("value = %q", result.Value)
	}
}

func TestStructuredReportsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer server.Close()

	var result struct{}
	err := New(server.URL, "", "m", "").Structured(context.Background(), "s", "u", "n", map[string]any{}, &result)
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestStructuredReportsEmptyAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":""}}]}`))
	}))
	defer server.Close()

	var result struct{}
	err := New(server.URL, "", "m", "").Structured(context.Background(), "s", "u", "n", map[string]any{}, &result)
	if err == nil || !strings.Contains(err.Error(), "empty answer") {
		t.Fatalf("err = %v", err)
	}
}

func TestModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen"},{"id":"gemma"}]}`))
	}))
	defer server.Close()

	models, err := New(server.URL+"/v1", "", "m", "").Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"gemma", "qwen"}; !slices.Equal(models, want) {
		t.Errorf("models = %v, want %v", models, want)
	}
}

func TestStructuredAcceptsFencedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + "```json\\n{\\\"value\\\":\\\"ok\\\"}\\n```" + `"}}]}`))
	}))
	defer server.Close()

	var result struct {
		Value string `json:"value"`
	}
	if err := New(server.URL, "", "m", "").Structured(context.Background(), "s", "u", "n", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "ok" {
		t.Errorf("value = %q", result.Value)
	}
}

func TestTranscribeSendsAudioAndJoinsParts(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request struct {
			Messages []struct {
				Content []contentPart `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		audio := request.Messages[0].Content[1].InputAudio
		if audio == nil || audio.Format != "wav" || audio.Data == "" {
			t.Errorf("audio part = %+v", audio)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" часть "}}]}`))
	}))
	defer server.Close()

	wav := encodeWAV(tone(100, 1000))
	text, err := New(server.URL, "", "m", "").Transcribe(context.Background(), wav)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || text != "часть часть" {
		t.Errorf("requests = %d, text = %q", requests, text)
	}
}

func TestTranscribeRejectsInvalidAudio(t *testing.T) {
	_, err := New("http://unused", "", "m", "").Transcribe(context.Background(), []byte("nope"))
	if !errors.Is(err, errInvalidAudio) {
		t.Errorf("err = %v", err)
	}
}
