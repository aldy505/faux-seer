package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aldy505/faux-seer/internal/assistedquery"
	"github.com/aldy505/faux-seer/internal/auth"
	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/explorer"
	"github.com/aldy505/faux-seer/internal/feedback"
	"github.com/aldy505/faux-seer/internal/generation"
	"github.com/aldy505/faux-seer/internal/investigations"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/monitoring"
	"github.com/aldy505/faux-seer/internal/preferences"
	"github.com/aldy505/faux-seer/internal/replay"
	"github.com/aldy505/faux-seer/internal/runmgr"
	"github.com/aldy505/faux-seer/internal/runs"
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
		SharedSecrets:         []string{"test-secret"},
		LLMModel:              []string{"test-model"},
		EmbeddingModel:        []string{"stub"},
		EmbeddingDimensions:   8,
		SimilarityThreshold:   0.1,
		VectorDimensions:      8,
		CodeIndexChunkSize:    1000,
		CodeIndexChunkOverlap: 200,
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
	runManager := runmgr.New(context.Background(), logger)
	runService := runs.New(store, llmClient, runManager)
	// No git provider: repository-backed endpoints answer with their simulated
	// responses, exactly as a deployment without repository credentials does.
	codeIndex := codeindex.New(nil, embClient, vectorStore, cfg.CodeIndexChunkSize, cfg.CodeIndexChunkOverlap)
	server := New(Services{
		Config:         cfg,
		Logger:         logger,
		Store:          store,
		LLM:            llmClient,
		CodeIndex:      codeIndex,
		Runs:           runManager,
		Autofix:        autofix.New(store, llmClient, runManager),
		Explorer:       explorer.New(store, llmClient, codeIndex, runManager),
		Similarity:     similarity.New(cfg, embClient, vectorStore),
		Severity:       severity.New(llmClient),
		IssueSummary:   issuesummary.New(llmClient),
		Feedback:       feedback.New(llmClient),
		Replay:         replay.New(store, llmClient),
		Generation:     generation.New(llmClient, nil, cfg),
		RunStore:       runService,
		Investigations: investigations.New(runService),
		AssistedQuery:  assistedquery.New(runService, codeIndex),
		Preferences:    preferences.New(store),
		Monitoring:     monitoring.New(cfg),
	})
	return server, store, llmClient, embClient, vectorStore, func() {
		runManager.CancelAll()
		_ = store.Close()
	}
}

func newSQLiteVectorServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.Config{SharedSecrets: []string{"test-secret"}, EmbeddingModel: []string{"stub"}, LLMModel: []string{"test-model"}, SimilarityThreshold: 0.1, VectorDimensions: 8, CodeIndexChunkSize: 1000, CodeIndexChunkOverlap: 200}
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
	runManager := runmgr.New(context.Background(), logger)
	runService := runs.New(store, llmClient, runManager)
	codeIndex := codeindex.New(nil, embClient, vectorStore, 1000, 200)
	t.Cleanup(runManager.CancelAll)
	return New(Services{
		Config:         cfg,
		Logger:         logger,
		Store:          store,
		LLM:            llmClient,
		CodeIndex:      codeIndex,
		Runs:           runManager,
		Autofix:        autofix.New(store, llmClient, runManager),
		Explorer:       explorer.New(store, llmClient, codeIndex, runManager),
		Similarity:     similarity.New(cfg, embClient, vectorStore),
		Severity:       severity.New(llmClient),
		IssueSummary:   issuesummary.New(llmClient),
		Feedback:       feedback.New(llmClient),
		Replay:         replay.New(store, llmClient),
		Generation:     generation.New(llmClient, nil, cfg),
		RunStore:       runService,
		Investigations: investigations.New(runService),
		AssistedQuery:  assistedquery.New(runService, codeIndex),
		Preferences:    preferences.New(store),
		Monitoring:     monitoring.New(cfg),
	})
}

func issueRequest(server *Server, method, target string, payload []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req.Header.Set("Authorization", auth.SignRequestBody(payload, "test-secret"))
	resp := httptest.NewRecorder()
	server.Routes().ServeHTTP(resp, req)
	return resp
}

// waitForExplorerRun blocks until a chat run leaves the processing status.
//
// Chat starts its reply generation in the background, so a test that asserts on
// the finished conversation has to wait for the run to reach a terminal state.
func waitForExplorerRun(t *testing.T, server *Server, organizationID, runID int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	payload := []byte(`{"organization_id":` + jsonNumber(organizationID) + `,"run_id":` + jsonNumber(runID) + `}`)
	for time.Now().Before(deadline) {
		resp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/state", payload)
		if resp.Code != http.StatusOK {
			t.Fatalf("explorer state returned %d: %s", resp.Code, resp.Body.String())
		}
		var body struct {
			Session *struct {
				Status string `json:"status"`
			} `json:"session"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode explorer state: %v", err)
		}
		if body.Session != nil && body.Session.Status != "processing" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("explorer run %d never left the processing status", runID)
}

func jsonNumber(value int64) string { return strings.TrimSpace(string(mustJSON(value))) }

func mustJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
