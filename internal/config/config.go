// Package config reads the server settings from the environment.
package config

import "os"

const (
	defaultAddr        = "127.0.0.1:8080"
	defaultDatabaseURL = "postgres://ordeus:ordeus@127.0.0.1:5432/ordeus?sslmode=disable"
	defaultAIBaseURL   = "http://127.0.0.1:11434/v1"
	defaultAIModel     = "gemma4:12b"
	defaultSecretFile  = ".ordeus-secret"
	defaultUploadsDir  = "uploads"
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
	// AIAllowPrivateURLs lets accounts use model servers on a private network.
	// Leave it off when the app is open to people you do not trust.
	AIAllowPrivateURLs bool
	// SecretFile holds the key that encrypts vendor API keys.
	SecretFile string
	// SecureCookies must be on whenever the app is served over HTTPS.
	SecureCookies bool
	// UploadsDir holds the pictures attached to notes. Back it up with the database.
	UploadsDir string
}

func Load() Config {
	baseURL := env("ORDEUS_AI_BASE_URL", defaultAIBaseURL)
	reasoningEffort := os.Getenv("ORDEUS_AI_REASONING_EFFORT")
	if reasoningEffort == "" && baseURL == defaultAIBaseURL {
		reasoningEffort = localReasoningEffort
	}

	return Config{
		Addr:               env("ORDEUS_ADDR", defaultAddr),
		DatabaseURL:        env("ORDEUS_DATABASE_URL", defaultDatabaseURL),
		AIBaseURL:          baseURL,
		AIAPIKey:           os.Getenv("ORDEUS_AI_API_KEY"),
		AIModel:            env("ORDEUS_AI_MODEL", defaultAIModel),
		AIReasoningEffort:  reasoningEffort,
		SecureCookies:      os.Getenv("ORDEUS_SECURE_COOKIES") == "true",
		AIAllowPrivateURLs: os.Getenv("ORDEUS_AI_ALLOW_PRIVATE_URLS") == "true",
		SecretFile:         env("ORDEUS_SECRET_FILE", defaultSecretFile),
		UploadsDir:         env("ORDEUS_UPLOADS_DIR", defaultUploadsDir),
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
