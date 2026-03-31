// Package llm contains an Anthropic-compatible client.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type anthropicClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

type anthropicRequest struct {
	Model     string `json:"model"`
	System    string `json:"system,omitempty"`
	MaxTokens int    `json:"max_tokens"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// NewAnthropicClient builds an Anthropic messages API client.
func NewAnthropicClient(baseURL, apiKey, model string) Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	return &anthropicClient{httpClient: &http.Client{}, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model}
}

func (c *anthropicClient) Complete(ctx context.Context, req CompletionRequest) (string, error) {
	payload, err := json.Marshal(anthropicRequest{Model: c.model, System: req.SystemPrompt, MaxTokens: req.MaxTokens, Messages: []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}{{Role: "user", Content: req.UserPrompt}}})
	if err != nil {
		return "", fmt.Errorf("marshal anthropic request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call anthropic API: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read anthropic response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("anthropic API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded anthropicResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	for _, part := range decoded.Content {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			return part.Text, nil
		}
	}
	return "", fmt.Errorf("anthropic API returned no text content")
}
