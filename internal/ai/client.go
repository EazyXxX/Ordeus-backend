// Package ai talks to any OpenAI-compatible chat API. That covers a local
// Ollama and external providers: only the base URL, key and model differ.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
)

const maxErrorBodyBytes = 500

type Client struct {
	baseURL string
	apiKey  string
	model   atomic.Pointer[string]
	// reasoningEffort is omitted from requests when empty, because not every
	// model accepts it.
	reasoningEffort string
	http            *http.Client
}

func New(baseURL, apiKey, model, reasoningEffort string) *Client {
	client := &Client{
		baseURL:         strings.TrimRight(baseURL, "/"),
		apiKey:          apiKey,
		reasoningEffort: reasoningEffort,
		http:            &http.Client{},
	}
	client.SetModel(model)
	return client
}

// Model returns the model used for new requests.
func (c *Client) Model() string {
	return *c.model.Load()
}

// SetModel switches the model. A request already in flight keeps its model.
func (c *Client) SetModel(model string) {
	c.model.Store(&model)
}

// Models lists the models the provider offers, sorted by name.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.authorize(request)

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list models: provider returned %d", response.StatusCode)
	}

	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	models := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		models = append(models, model.ID)
	}
	slices.Sort(models)
	return models, nil
}

func (c *Client) authorize(request *http.Request) {
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type jsonSchemaFormat struct {
	Name   string `json:"name"`
	Strict bool   `json:"strict"`
	Schema any    `json:"schema"`
}

type responseFormat struct {
	Type       string           `json:"type"`
	JSONSchema jsonSchemaFormat `json:"json_schema"`
}

type chatRequest struct {
	Model           string         `json:"model"`
	Temperature     float64        `json:"temperature"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	Messages        []message      `json:"messages"`
	ResponseFormat  responseFormat `json:"response_format"`
}

type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

// Structured asks the model for JSON matching schema and decodes it into target.
func (c *Client) Structured(ctx context.Context, system, user, name string, schema, target any) error {
	body, err := json.Marshal(chatRequest{
		Model:           c.Model(),
		Temperature:     0.2,
		ReasoningEffort: c.reasoningEffort,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ResponseFormat: responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchemaFormat{Name: name, Strict: true, Schema: schema},
		},
	})
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	c.authorize(request)

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("model request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
		return fmt.Errorf("model returned %d: %s", response.StatusCode, detail)
	}

	var chat chatResponse
	if err := json.NewDecoder(response.Body).Decode(&chat); err != nil {
		return fmt.Errorf("decode model response: %w", err)
	}
	if len(chat.Choices) == 0 {
		return errors.New("model returned no choices")
	}
	content := chat.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return errors.New("model returned an empty answer")
	}
	if err := json.Unmarshal([]byte(content), target); err != nil {
		return fmt.Errorf("decode structured output: %w", err)
	}
	return nil
}
