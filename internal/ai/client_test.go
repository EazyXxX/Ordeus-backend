package ai

import (
	"context"
	"encoding/json"
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

func TestSetModelAppliesToNextRequest(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		seen = request.Model
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer server.Close()

	client := New(server.URL, "", "first", "")
	client.SetModel("second")
	var result struct{}
	if err := client.Structured(context.Background(), "s", "u", "n", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if seen != "second" {
		t.Errorf("model = %q", seen)
	}
}
