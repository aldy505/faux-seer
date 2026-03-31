// Package llm creates the configured text generation client.
package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/config"
)

type stubClient struct{}

// New returns the configured LLM client.
func New(cfg *config.Config) (Client, error) {
	switch strings.ToLower(cfg.LLMProvider) {
	case "", "stub":
		return stubClient{}, nil
	case "openai":
		return NewOpenAICompatClient("https://api.openai.com/v1", cfg.LLMAPIKey, cfg.LLMModel, cfg.HTTPReferer), nil
	case "openrouter", "custom":
		return NewOpenAICompatClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel, cfg.HTTPReferer), nil
	case "anthropic":
		return NewAnthropicClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider %q", cfg.LLMProvider)
	}
}

func (stubClient) Complete(_ context.Context, req CompletionRequest) (string, error) {
	return "Stub provider response:\n\n" + strings.TrimSpace(req.UserPrompt), nil
}
