package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSeverityScoreReturnsClampedResponse(t *testing.T) {
	server := newTestServer(t)
	payload := []byte(`{"message":"panic fatal crash out of memory deadlock segfault","has_stacktrace":1,"handled":false}`)

	resp := issueRequest(server, http.MethodPost, "/v0/issues/severity-score", payload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Severity float64 `json:"severity"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode severity response: %v", err)
	}
	if body.Severity != 1 {
		t.Fatalf("expected clamped severity 1, got %v", body.Severity)
	}
}
