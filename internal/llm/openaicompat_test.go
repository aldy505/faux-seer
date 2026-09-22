package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatClientSendsExpectedHeaders(t *testing.T) {
	var gotAuth, gotReferer, gotPath string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotReferer = r.Header.Get("HTTP-Referer")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "done"}},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatClient(server.URL, "test-key", []string{"gpt-test"}, "https://example.com")
	text, err := client.Complete(context.Background(), CompletionRequest{
		SystemPrompt: "system",
		UserPrompt:   "user",
		Temperature:  0.2,
		MaxTokens:    128,
	})
	if err != nil {
		t.Fatalf("complete request: %v", err)
	}
	if text != "done" {
		t.Fatalf("expected done, got %q", text)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("expected bearer auth, got %q", gotAuth)
	}
	if gotReferer != "https://example.com" {
		t.Fatalf("expected HTTP-Referer, got %q", gotReferer)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("expected chat completions path, got %q", gotPath)
	}
	if gotBody["model"] != "gpt-test" {
		t.Fatalf("expected model in request body, got %#v", gotBody)
	}
}

func TestOpenAICompatClientRoundRobinsModels(t *testing.T) {
	var models []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		models = append(models, body["model"].(string))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "done"}},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatClient(server.URL, "test-key", []string{"model-a", "model-b"}, "")
	for i := range 5 {
		if _, err := client.Complete(context.Background(), CompletionRequest{UserPrompt: "hi"}); err != nil {
			t.Fatalf("complete request %d: %v", i, err)
		}
	}

	want := []string{"model-a", "model-b", "model-a", "model-b", "model-a"}
	if len(models) != len(want) {
		t.Fatalf("expected %d requests, got %d", len(want), len(models))
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("request %d used model %q, want %q (all: %#v)", i, models[i], want[i], models)
		}
	}
}
