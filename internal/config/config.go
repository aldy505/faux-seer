// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for faux-seer.
type Config struct {
	Addr                 string
	LogLevel             string
	DatabasePath         string
	SharedSecrets        []string
	SentryDSN            string
	SentrySampleRate     float64
	SentryTracesRate     float64
	SentrySendDefaultPII bool
	LLMProvider          string
	LLMBaseURL           string
	LLMAPIKey            string
	LLMModel             string
	LLMMaxTokens         int
	LLMTemperature       float64
	EmbeddingProvider    string
	EmbeddingBaseURL     string
	EmbeddingAPIKey      string
	EmbeddingModel       string
	EmbeddingDimensions  int
	VectorStore          string
	VectorStoreDSN       string
	VectorDimensions     int
	SimilarityThreshold  float64
	HTTPReferer          string
}

// Load reads environment variables and returns validated configuration.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:              envDefault("ADDR", ":9091"),
		LogLevel:          envDefault("LOG_LEVEL", "info"),
		DatabasePath:      envDefault("DATABASE_PATH", filepath.Join("data", "faux-seer.db")),
		SentryDSN:         strings.TrimSpace(os.Getenv("SENTRY_DSN")),
		LLMProvider:       strings.ToLower(envDefault("LLM_PROVIDER", "stub")),
		LLMBaseURL:        strings.TrimSpace(os.Getenv("LLM_BASE_URL")),
		LLMAPIKey:         strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		LLMModel:          envDefault("LLM_MODEL", "gpt-4.1-mini"),
		EmbeddingProvider: strings.ToLower(envDefault("EMBEDDING_PROVIDER", "stub")),
		EmbeddingBaseURL:  strings.TrimSpace(os.Getenv("EMBEDDING_BASE_URL")),
		EmbeddingAPIKey:   strings.TrimSpace(os.Getenv("EMBEDDING_API_KEY")),
		EmbeddingModel:    envDefault("EMBEDDING_MODEL", "text-embedding-3-small"),
		VectorStore:       strings.ToLower(envDefault("VECTOR_STORE", "sqlitevec")),
		VectorStoreDSN:    strings.TrimSpace(os.Getenv("VECTOR_STORE_DSN")),
		HTTPReferer:       strings.TrimSpace(os.Getenv("HTTP_REFERER")),
		SharedSecrets:     splitSecrets(firstNonEmpty(os.Getenv("SEER_SHARED_SECRET"), os.Getenv("SEER_RPC_SHARED_SECRET"), os.Getenv("SEER_API_SHARED_SECRET"), os.Getenv("SHARED_SECRET"))),
	}

	var errs []string
	cfg.SentrySampleRate = floatDefault("SENTRY_SAMPLE_RATE", 1.0, &errs)
	cfg.SentryTracesRate = floatDefault("SENTRY_TRACES_SAMPLE_RATE", 1.0, &errs)
	cfg.SentrySendDefaultPII = boolDefault("SENTRY_SEND_DEFAULT_PII", false, &errs)
	cfg.LLMMaxTokens = intDefault("LLM_MAX_TOKENS", 2048, &errs)
	cfg.LLMTemperature = floatDefault("LLM_TEMPERATURE", 0.2, &errs)
	cfg.EmbeddingDimensions = intDefault("EMBEDDING_DIMENSIONS", 256, &errs)
	cfg.VectorDimensions = intDefault("VECTOR_DIMENSIONS", cfg.EmbeddingDimensions, &errs)
	cfg.SimilarityThreshold = floatDefault("SIMILARITY_THRESHOLD", 0.1, &errs)

	if cfg.VectorStore != "sqlitevec" && cfg.VectorStore != "pgvector" {
		errs = append(errs, "VECTOR_STORE must be one of sqlitevec or pgvector")
	}
	if cfg.VectorStore == "pgvector" && cfg.VectorStoreDSN == "" {
		errs = append(errs, "VECTOR_STORE_DSN is required when VECTOR_STORE=pgvector")
	}
	if cfg.VectorDimensions <= 0 {
		errs = append(errs, "VECTOR_DIMENSIONS must be greater than zero")
	}
	if cfg.SentrySampleRate < 0 || cfg.SentrySampleRate > 1 {
		errs = append(errs, "SENTRY_SAMPLE_RATE must be between 0 and 1")
	}
	if cfg.SentryTracesRate < 0 || cfg.SentryTracesRate > 1 {
		errs = append(errs, "SENTRY_TRACES_SAMPLE_RATE must be between 0 and 1")
	}
	if cfg.LLMProvider == "openrouter" || cfg.LLMProvider == "custom" || cfg.LLMProvider == "openai" {
		if cfg.LLMProvider != "openai" && cfg.LLMBaseURL == "" {
			errs = append(errs, "LLM_BASE_URL is required for openrouter/custom")
		}
	}
	if cfg.EmbeddingProvider == "openrouter" || cfg.EmbeddingProvider == "custom" || cfg.EmbeddingProvider == "openai" {
		if cfg.EmbeddingProvider != "openai" && cfg.EmbeddingBaseURL == "" {
			errs = append(errs, "EMBEDDING_BASE_URL is required for openrouter/custom")
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return cfg, nil
}

func envDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func intDefault(key string, fallback int, errs *[]string) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s must be an integer", key))
		return fallback
	}
	return parsed
}

func floatDefault(key string, fallback float64, errs *[]string) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s must be a float", key))
		return fallback
	}
	return parsed
}

func boolDefault(key string, fallback bool, errs *[]string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s must be a boolean", key))
		return fallback
	}
	return parsed
}

func splitSecrets(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' })
	secrets := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			secrets = append(secrets, trimmed)
		}
	}
	return secrets
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
