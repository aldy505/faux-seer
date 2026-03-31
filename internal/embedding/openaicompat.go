// Package embedding implements OpenAI-compatible embedding clients.
package embedding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

type openAICompatClient struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	model       string
	dimensions  int
	httpReferer string
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

type stubClient struct{ dimensions int }

// NewOpenAICompatClient creates an OpenAI-compatible embedding client.
func NewOpenAICompatClient(baseURL, apiKey, model string, dimensions int, httpReferer string) Client {
	return &openAICompatClient{httpClient: &http.Client{}, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, model: model, dimensions: dimensions, httpReferer: httpReferer}
}

// NewStubClient creates a deterministic local embedding client.
func NewStubClient(dimensions int) Client { return &stubClient{dimensions: dimensions} }

func (c *openAICompatClient) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	payload, err := json.Marshal(embeddingRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embedding request: %w", err)
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
		return nil, fmt.Errorf("call embedding API: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embedding response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("embedding API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded embeddingResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	vectors := make([][]float32, 0, len(decoded.Data))
	for _, item := range decoded.Data {
		vectors = append(vectors, item.Embedding)
	}
	return vectors, nil
}

func (c *stubClient) EmbedTexts(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vec := make([]float32, c.dimensions)
		for i := 0; i < c.dimensions; i++ {
			hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", i, text)))
			value := float64(binary.BigEndian.Uint32(hash[:4])) / float64(math.MaxUint32)
			vec[i] = float32((value * 2) - 1)
		}
		normalize(vec)
		vectors = append(vectors, vec)
	}
	return vectors, nil
}

func normalize(vec []float32) {
	var sum float64
	for _, value := range vec {
		sum += float64(value * value)
	}
	if sum == 0 {
		return
	}
	magnitude := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= magnitude
	}
}
