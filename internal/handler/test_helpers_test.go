package handler

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/aldy505/faux-seer/internal/auth"
	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/severity"
	"github.com/aldy505/faux-seer/internal/similarity"
	"github.com/aldy505/faux-seer/internal/testutil"
	"github.com/aldy505/faux-seer/internal/vectorstore/sqlitevec"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server, _, _, _, _, cleanup := newTestServerWithMocks(t)
	t.Cleanup(cleanup)
	return server
}

func newTestServerWithMocks(t *testing.T) (*Server, *db.Store, *testutil.MockLLMClient, *testutil.MockEmbeddingClient, *testutil.MockVectorStore, func()) {
	t.Helper()
	cfg := &config.Config{
		SharedSecrets:       []string{"test-secret"},
		EmbeddingModel:      "stub",
		EmbeddingDimensions: 8,
		SimilarityThreshold: 0.1,
	}
	store, err := db.New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	llmClient := &testutil.MockLLMClient{
		CompleteFunc: func(_ context.Context, req llm.CompletionRequest) (string, error) {
			return "Stub provider response:\n\n" + req.UserPrompt, nil
		},
	}
	embClient := &testutil.MockEmbeddingClient{Dimensions: 8}
	vectorStore := &testutil.MockVectorStore{}
	server := New(
		cfg,
		logger,
		store,
		autofix.New(store, llmClient),
		similarity.New(cfg, embClient, vectorStore),
		severity.New(llmClient),
		issuesummary.New(llmClient),
	)
	return server, store, llmClient, embClient, vectorStore, func() { _ = store.Close() }
}

func newSQLiteVectorServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{SharedSecrets: []string{"test-secret"}, EmbeddingModel: "stub", SimilarityThreshold: 0.1, VectorDimensions: 8}
	store, err := db.New(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	llmClient, _ := llm.New(&config.Config{LLMProvider: "stub"})
	embClient := &testutil.MockEmbeddingClient{Dimensions: 8}
	vectorStore, err := sqlitevec.New(context.Background(), store, 8)
	if err != nil {
		t.Fatalf("create sqlitevec store: %v", err)
	}
	return New(cfg, logger, store, autofix.New(store, llmClient), similarity.New(cfg, embClient, vectorStore), severity.New(llmClient), issuesummary.New(llmClient))
}

func issueRequest(server *Server, method, target string, payload []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req.Header.Set("Authorization", auth.SignRequestBody(payload, "test-secret"))
	resp := httptest.NewRecorder()
	server.Routes().ServeHTTP(resp, req)
	return resp
}
