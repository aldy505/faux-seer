package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatClientRoundRobinsModels(t *testing.T) {
	var models []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		models = append(models, body.Model)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"embedding": []float32{1, 0}},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatClient(server.URL, "test-key", []string{"embed-a", "embed-b"}, 2, "", nil)
	for i := range 3 {
		if _, err := client.EmbedTexts(context.Background(), []string{"text"}); err != nil {
			t.Fatalf("embed request %d: %v", i, err)
		}
	}

	want := []string{"embed-a", "embed-b", "embed-a"}
	if len(models) != len(want) {
		t.Fatalf("expected %d requests, got %d", len(want), len(models))
	}
	for i := range want {
		if models[i] != want[i] {
			t.Fatalf("request %d used model %q, want %q (all: %#v)", i, models[i], want[i], models)
		}
	}
}
