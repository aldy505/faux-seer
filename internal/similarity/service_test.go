package similarity

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/embedding"
	"github.com/aldy505/faux-seer/internal/vectorstore/sqlitevec"
)

func TestSimilarityTrainingAndLookup(t *testing.T) {
	store, err := db.New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	vectorStore, err := sqlitevec.New(context.Background(), store, 12)
	if err != nil {
		t.Fatalf("create sqlitevec store: %v", err)
	}
	service := New(&config.Config{SimilarityThreshold: 0.2, EmbeddingModel: "stub"}, embedding.NewStubClient(12), vectorStore)
	seed := []byte(`{"project_id":1,"stacktrace":"panic at foo","hash":"hash-a","training_mode":true}`)
	if _, err := service.Similar(context.Background(), seed); err != nil {
		t.Fatalf("seed similarity record: %v", err)
	}
	query := []byte(`{"project_id":1,"stacktrace":"panic at foo","hash":"hash-b","k":1}`)
	response, err := service.Similar(context.Background(), query)
	if err != nil {
		t.Fatalf("query similarity: %v", err)
	}
	if len(response.Responses) == 0 {
		t.Fatalf("expected at least one similar result")
	}
	payload, _ := json.Marshal(response)
	if len(payload) == 0 {
		t.Fatal("expected marshaled similarity response")
	}
}
