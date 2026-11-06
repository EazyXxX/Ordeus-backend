// Package ai talks to any OpenAI-compatible chat API. That covers a local
// Ollama and external providers: only the base URL, key and model differ.
package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

const (
	maxErrorBodyBytes     = 500
	maxResponseBytes      = 16 << 20
	structuredTemperature = 0.2
	transcribePrompt      = "Расшифруй эту аудиозапись дословно на языке оригинала. Верни только текст речи, без комментариев."
)

// Client talks to one model at one address. Accounts choose their own, so
// clients are built per use and hold no shared state.
type Client struct {
	baseURL string
	apiKey  string
	model   string
	// reasoningEffort is omitted from requests when empty, because not every
	// model accepts it.
	reasoningEffort string
	http            *http.Client
}

func New(baseURL, apiKey, model, reasoningEffort string) *Client {
	return &Client{
		baseURL:         strings.TrimRight(baseURL, "/"),
		apiKey:          apiKey,
		model:           model,
		reasoningEffort: reasoningEffort,
		http:            &http.Client{},
	}
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
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&list); err != nil {
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

// message content is text, or a list of parts for audio.
type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type contentPart struct {
	Type       string      `json:"type"`
	Text       string      `json:"text,omitempty"`
	InputAudio *inputAudio `json:"input_audio,omitempty"`
}

type inputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
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
	Model           string          `json:"model"`
	Temperature     float64         `json:"temperature"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Messages        []message       `json:"messages"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Structured asks the model for JSON matching schema and decodes it into target.
func (c *Client) Structured(ctx context.Context, system, user, name string, schema, target any) error {
	content, err := c.complete(ctx, chatRequest{
		Temperature: structuredTemperature,
		Messages: []message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		ResponseFormat: &responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchemaFormat{Name: name, Strict: true, Schema: schema},
		},
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(stripCodeFence(content)), target); err != nil {
		return fmt.Errorf("decode structured output: %w", err)
	}
	return nil
}

// Transcribe turns recorded speech into text. Long audio goes to the model in
// parts, because a single request has to fit the model's context.
func (c *Client) Transcribe(ctx context.Context, wav []byte) (string, error) {
	pcm, err := parseWAV(wav)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, chunk := range splitPCM(pcm, maxChunkBytes) {
		text, err := c.transcribeChunk(ctx, chunk)
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.TrimSpace(text))
	}
	return strings.TrimSpace(strings.Join(parts, " ")), nil
}

func (c *Client) transcribeChunk(ctx context.Context, pcm []byte) (string, error) {
	return c.complete(ctx, chatRequest{
		Messages: []message{{
			Role: "user",
			Content: []contentPart{
				{Type: "text", Text: transcribePrompt},
				{Type: "input_audio", InputAudio: &inputAudio{
					Data:   base64.StdEncoding.EncodeToString(encodeWAV(pcm)),
					Format: "wav",
				}},
			},
		}},
	})
}

// hideKey keeps the key out of error text that gets logged and stored.
func (c *Client) hideKey(text string) string {
	if c.apiKey == "" {
		return text
	}
	return strings.ReplaceAll(text, c.apiKey, "***")
}

// stripCodeFence removes the markdown fence some models put around JSON.
func stripCodeFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	_, content, _ = strings.Cut(content, "\n")
	return strings.TrimSuffix(strings.TrimSpace(content), "```")
}

// complete sends a chat request with the current model and returns the answer.
func (c *Client) complete(ctx context.Context, chat chatRequest) (string, error) {
	chat.Model = c.model
	chat.ReasoningEffort = c.reasoningEffort

	body, err := json.Marshal(chat)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	c.authorize(request)

	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("model request: %w", errors.New(c.hideKey(err.Error())))
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes))
		return "", fmt.Errorf("model returned %d: %s", response.StatusCode, c.hideKey(string(detail)))
	}

	var answer chatResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&answer); err != nil {
		return "", fmt.Errorf("decode model response: %w", err)
	}
	if len(answer.Choices) == 0 {
		return "", errors.New("model returned no choices")
	}
	content := answer.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", errors.New("model returned an empty answer")
	}
	return content, nil
}
