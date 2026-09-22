// Package llm creates the configured text generation client.
package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/httpclient"
)

type stubClient struct{}

// New returns the configured LLM client.
func New(cfg *config.Config) (Client, error) {
	httpClient := httpclient.New(cfg)
	switch strings.ToLower(cfg.LLMProvider) {
	case "", "stub":
		return stubClient{}, nil
	case "openai":
		return NewOpenAICompatClient("https://api.openai.com/v1", cfg.LLMAPIKey, cfg.LLMModel, cfg.HTTPReferer, httpClient), nil
	case "openrouter", "custom":
		return NewOpenAICompatClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.HTTPReferer, httpClient), nil
	case "anthropic":
		return NewAnthropicClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, httpClient), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.LLMProvider)
	}
}

func (stubClient) Complete(_ context.Context, req CompletionRequest) (string, error) {
	return "Stub provider response:\n\n" + strings.TrimSpace(req.UserPrompt), nil
}
