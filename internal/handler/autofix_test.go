package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAutofixStartReturnsRunID(t *testing.T) {
	server := newTestServer(t)
	payload := []byte(`{
		"organization_id": 1,
		"project_id": 2,
		"issue": {"id": 123, "title": "TypeError in checkout"},
		"repos": [{"provider":"github","owner":"acme","name":"app","external_id":"42"}]
	}`)

	resp := issueRequest(server, http.MethodPost, "/v1/automation/autofix/start", payload)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Started bool  `json:"started"`
		RunID   int64 `json:"run_id"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if !body.Started {
		t.Fatal("expected started=true")
	}
	if body.RunID <= 0 {
		t.Fatalf("expected positive run_id, got %d", body.RunID)
	}
}

func TestAutofixStateReturnsPersistedShape(t *testing.T) {
	server := newTestServer(t)
	startPayload := []byte(`{
		"organization_id": 1,
		"project_id": 2,
		"issue": {"id": 123, "title": "TypeError in checkout"},
		"repos": [{"provider":"github","owner":"acme","name":"app","external_id":"42"}]
	}`)
	startResp := issueRequest(server, http.MethodPost, "/v1/automation/autofix/start", startPayload)

	var started struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}

	stateResp := issueRequest(server, http.MethodPost, "/v1/automation/autofix/state", []byte(`{"run_id":`+jsonNumber(started.RunID)+`,"group_id":123}`))
	if stateResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", stateResp.Code, stateResp.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(stateResp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	state, ok := body["state"].(map[string]any)
	if !ok {
		t.Fatalf("expected state object, got %#v", body["state"])
	}
	if state["status"] != "COMPLETED" {
		t.Fatalf("expected COMPLETED status, got %#v", state["status"])
	}
	steps, ok := state["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected non-empty steps, got %#v", state["steps"])
	}
}

func TestAutofixStartRejectsBadBody(t *testing.T) {
	server := newTestServer(t)
	resp := issueRequest(server, http.MethodPost, "/v1/automation/autofix/start", []byte(`{"organization_id":`))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "decode autofix start request") {
		t.Fatalf("expected decode error, got %s", resp.Body.String())
	}
}

func jsonNumber(value int64) string { return strings.TrimSpace(string(mustJSON(value))) }

func mustJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}
