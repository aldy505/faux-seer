// Package embedding creates the configured embedding client.
package embedding

import (
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/httpclient"
)

// New returns the configured embedding client.
func New(cfg *config.Config) (Client, error) {
	httpClient := httpclient.New(cfg)
	switch strings.ToLower(cfg.EmbeddingProvider) {
	case "", "stub":
		return NewStubClient(cfg.EmbeddingDimensions), nil
	case "openai":
		return NewOpenAICompatClient("https://api.openai.com/v1", cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDimensions, cfg.HTTPReferer, httpClient), nil
	case "openrouter", "custom":
		return NewOpenAICompatClient(cfg.EmbeddingBaseURL, cfg.EmbeddingAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDimensions, cfg.HTTPReferer, httpClient), nil
	default:
		return nil, fmt.Errorf("unsupported embedding provider %q", cfg.EmbeddingProvider)
	}
}
