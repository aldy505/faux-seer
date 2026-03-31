package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aldy505/faux-seer/internal/testutil"
	"github.com/aldy505/faux-seer/internal/vectorstore"
)

func TestSimilarityHandlerRoutesToStore(t *testing.T) {
	server, _, _, embeddings, store, cleanup := newTestServerWithMocks(t)
	defer cleanup()

	embeddings.EmbedFunc = func(_ context.Context, texts []string) ([][]float32, error) {
		return [][]float32{{1, 0, 0, 0, 0, 0, 0, 0}}, nil
	}
	store.SearchSimilarFunc = func(_ context.Context, projectID int64, hash string, vector []float32, k int, threshold float64) ([]vectorstore.SimilarIssue, error) {
		store.SearchCalls = append(store.SearchCalls, testutil.SearchCall{ProjectID: projectID, Hash: hash, Vector: vector, K: k, Threshold: threshold})
		return []vectorstore.SimilarIssue{{
			ParentHash:         "existing-hash",
			StacktraceDistance: 0.04,
			ShouldGroup:        true,
		}}, nil
	}

	payload := []byte(`{"project_id":1,"stacktrace":"TypeError: undefined is not a function","hash":"grouping-hash","k":3,"threshold":0.1}`)
	resp := issueRequest(server, http.MethodPost, "/v0/issues/similar-issues", payload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	if len(store.SearchCalls) != 1 {
		t.Fatalf("expected one search call, got %d", len(store.SearchCalls))
	}
	call := store.SearchCalls[0]
	if call.ProjectID != 1 || call.Hash != "grouping-hash" || call.K != 3 || call.Threshold != 0.1 {
		t.Fatalf("unexpected search call: %#v", call)
	}

	var body struct {
		Responses []vectorstore.SimilarIssue `json:"responses"`
		ModelUsed string                     `json:"model_used"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode similarity response: %v", err)
	}
	if len(body.Responses) != 1 || body.Responses[0].ParentHash != "existing-hash" {
		t.Fatalf("unexpected response payload: %#v", body)
	}
	if body.ModelUsed != "stub" {
		t.Fatalf("expected model_used stub, got %q", body.ModelUsed)
	}
}
