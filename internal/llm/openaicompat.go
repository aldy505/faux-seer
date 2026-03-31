// Package llm implements OpenAI-compatible chat completion clients.
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

type openAICompatClient struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	model       string
	httpReferer string
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// NewOpenAICompatClient builds an OpenAI-compatible chat completion client.
func NewOpenAICompatClient(baseURL, apiKey, model, httpReferer string) Client {
	return &openAICompatClient{
		httpClient:  &http.Client{},
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      apiKey,
		model:       model,
		httpReferer: httpReferer,
	}
}

func (c *openAICompatClient) Complete(ctx context.Context, req CompletionRequest) (string, error) {
	payload, err := json.Marshal(chatCompletionRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "system", Content: req.SystemPrompt}, {Role: "user", Content: req.UserPrompt}},
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	})
	if err != nil {
		return "", fmt.Errorf("marshal chat completion request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build chat completion request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if c.httpReferer != "" {
		httpReq.Header.Set("HTTP-Referer", c.httpReferer)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call chat completion API: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read chat completion response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("chat completion API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded chatCompletionResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("decode chat completion response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("chat completion API returned no choices")
	}
	return decoded.Choices[0].Message.Content, nil
}
