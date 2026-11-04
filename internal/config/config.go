// Package config reads the server settings from the environment.
package config

import "os"

const (
	defaultAddr        = "127.0.0.1:8080"
	defaultDatabaseURL = "postgres://ordeus:ordeus@127.0.0.1:5432/ordeus?sslmode=disable"
	defaultAIBaseURL   = "http://127.0.0.1:11434/v1"
	defaultAIModel     = "gemma4:12b"
	// Local reasoning models can spend minutes thinking and return no answer.
	localReasoningEffort = "none"
)

type Config struct {
	Addr        string
	DatabaseURL string
	AIBaseURL   string
	AIAPIKey    string
	AIModel     string
	// AIReasoningEffort is left out of requests when empty.
	AIReasoningEffort string
}

func Load() Config {
	baseURL := env("ORDEUS_AI_BASE_URL", defaultAIBaseURL)
	reasoningEffort := os.Getenv("ORDEUS_AI_REASONING_EFFORT")
	if reasoningEffort == "" && baseURL == defaultAIBaseURL {
		reasoningEffort = localReasoningEffort
	}

	return Config{
		Addr:              env("ORDEUS_ADDR", defaultAddr),
		DatabaseURL:       env("ORDEUS_DATABASE_URL", defaultDatabaseURL),
		AIBaseURL:         baseURL,
		AIAPIKey:          os.Getenv("ORDEUS_AI_API_KEY"),
		AIModel:           env("ORDEUS_AI_MODEL", defaultAIModel),
		AIReasoningEffort: reasoningEffort,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
