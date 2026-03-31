package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aldy505/faux-seer/internal/llm"
)

func TestIssueSummaryReturnsAllExpectedFields(t *testing.T) {
	server, _, llmClient, _, _, cleanup := newTestServerWithMocks(t)
	defer cleanup()

	llmClient.CompleteFunc = func(_ context.Context, req llm.CompletionRequest) (string, error) {
		return "Checkout failures are caused by a nil cart reference. Review the failing path.", nil
	}

	payload := []byte(`{
		"group_id":"123",
		"issue":{"title":"TypeError in checkout"},
		"trace_tree":{"id":"trace-1"}
	}`)
	resp := issueRequest(server, http.MethodPost, "/v1/automation/summarize/issue", payload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode issue summary response: %v", err)
	}
	for _, key := range []string{"group_id", "headline", "whats_wrong", "trace", "possible_cause", "scores"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("expected response key %q in %#v", key, body)
		}
	}
}
