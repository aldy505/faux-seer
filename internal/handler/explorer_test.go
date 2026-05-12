package handler

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestExplorerChatStartAndState(t *testing.T) {
	server := newTestServer(t)
	startPayload := []byte(`{
		"organization_id":1,
		"query":"What are my slowest DB queries?",
		"page_name":"/issues/:groupId/",
		"on_page_context":"Trace: abc123",
		"user_org_context":{"user_id":7}
	}`)
	startResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat", startPayload)
	if startResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", startResp.Code, startResp.Body.String())
	}
	var started struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if started.RunID == 0 {
		t.Fatalf("expected non-zero run_id, got %#v", started)
	}

	statePayload := []byte(`{"organization_id":1,"run_id":` + jsonNumber(started.RunID) + `}`)
	stateResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/state", statePayload)
	if stateResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", stateResp.Code, stateResp.Body.String())
	}
	var state struct {
		Session struct {
			RunID  int64 `json:"run_id"`
			Blocks []struct {
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
			} `json:"blocks"`
			Status      string         `json:"status"`
			OwnerUserID *int64         `json:"owner_user_id"`
			RepoPRs     map[string]any `json:"repo_pr_states"`
		} `json:"session"`
	}
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	if state.Session.RunID != started.RunID {
		t.Fatalf("expected run_id %d, got %d", started.RunID, state.Session.RunID)
	}
	if len(state.Session.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(state.Session.Blocks))
	}
	if state.Session.Blocks[0].Message.Role != "user" {
		t.Fatalf("expected first block to be user, got %#v", state.Session.Blocks[0].Message)
	}
	if state.Session.Blocks[1].Message.Role != "assistant" {
		t.Fatalf("expected second block to be assistant, got %#v", state.Session.Blocks[1].Message)
	}
	if state.Session.Status != "completed" {
		t.Fatalf("expected completed status, got %q", state.Session.Status)
	}
	if state.Session.OwnerUserID == nil || *state.Session.OwnerUserID != 7 {
		t.Fatalf("expected owner_user_id 7, got %#v", state.Session.OwnerUserID)
	}
	if len(state.Session.RepoPRs) != 0 {
		t.Fatalf("expected empty repo_pr_states, got %#v", state.Session.RepoPRs)
	}
}

func TestExplorerChatContinueAndRuns(t *testing.T) {
	server := newTestServer(t)
	firstResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat", []byte(`{
		"organization_id":1,
		"query":"First question",
		"category_key":"issue",
		"category_value":"554844",
		"user_org_context":{"user_id":7}
	}`))
	if firstResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", firstResp.Code, firstResp.Body.String())
	}
	var started struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(firstResp.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	continuePayload := []byte(`{
		"organization_id":1,
		"run_id":` + jsonNumber(started.RunID) + `,
		"query":"Follow-up question"
	}`)
	continueResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat", continuePayload)
	if continueResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", continueResp.Code, continueResp.Body.String())
	}

	stateResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/state", []byte(`{"organization_id":1,"run_id":`+jsonNumber(started.RunID)+`}`))
	var state struct {
		Session struct {
			Blocks []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"blocks"`
		} `json:"session"`
	}
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode continued state response: %v", err)
	}
	if len(state.Session.Blocks) != 4 {
		t.Fatalf("expected 4 blocks after continue, got %d", len(state.Session.Blocks))
	}
	if state.Session.Blocks[2].Message.Content != "Follow-up question" {
		t.Fatalf("expected continued user message, got %#v", state.Session.Blocks[2].Message)
	}

	_ = issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat", []byte(`{
		"organization_id":1,
		"query":"Another run",
		"user_org_context":{"user_id":8}
	}`))
	runsResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/runs", []byte(`{
		"organization_id":1,
		"user_id":7,
		"category_key":"issue",
		"category_value":"554844",
		"offset":0,
		"limit":10
	}`))
	if runsResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", runsResp.Code, runsResp.Body.String())
	}
	var runs struct {
		Data []struct {
			RunID         int64  `json:"run_id"`
			Title         string `json:"title"`
			CategoryKey   string `json:"category_key"`
			CategoryValue string `json:"category_value"`
			UserID        int64  `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(runsResp.Body.Bytes(), &runs); err != nil {
		t.Fatalf("decode runs response: %v", err)
	}
	if len(runs.Data) != 1 {
		t.Fatalf("expected 1 filtered run, got %d", len(runs.Data))
	}
	if runs.Data[0].RunID != started.RunID {
		t.Fatalf("expected run_id %d, got %d", started.RunID, runs.Data[0].RunID)
	}
	if runs.Data[0].UserID != 7 {
		t.Fatalf("expected user_id 7, got %d", runs.Data[0].UserID)
	}
	if runs.Data[0].CategoryKey != "issue" || runs.Data[0].CategoryValue != "554844" {
		t.Fatalf("expected category filter fields, got %#v", runs.Data[0])
	}
}

func TestExplorerUpdateAndStatePR(t *testing.T) {
	server := newTestServer(t)
	startResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat", []byte(`{
		"organization_id":1,
		"query":"Open a PR for this fix"
	}`))
	var started struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	updateResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/update", []byte(`{
		"organization_id":1,
		"run_id":`+jsonNumber(started.RunID)+`,
		"payload":{"type":"create_pr","repo_name":"getsentry/faux-seer"}
	}`))
	if updateResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", updateResp.Code, updateResp.Body.String())
	}
	stateResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/state", []byte(`{"organization_id":1,"run_id":`+jsonNumber(started.RunID)+`}`))
	var state struct {
		Session struct {
			RepoPRStates map[string]struct {
				RepoName         string `json:"repo_name"`
				PRCreationStatus string `json:"pr_creation_status"`
			} `json:"repo_pr_states"`
		} `json:"session"`
	}
	if err := json.Unmarshal(stateResp.Body.Bytes(), &state); err != nil {
		t.Fatalf("decode state response: %v", err)
	}
	prState, ok := state.Session.RepoPRStates["getsentry/faux-seer"]
	if !ok {
		t.Fatalf("expected repo_pr_states entry, got %#v", state.Session.RepoPRStates)
	}
	if prState.PRCreationStatus != "completed" {
		t.Fatalf("expected completed pr status, got %#v", prState)
	}

	statePRResp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/state/pr", []byte(`{
		"organization_id":1,
		"provider":"github",
		"pr_id":123
	}`))
	if statePRResp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", statePRResp.Code, statePRResp.Body.String())
	}
	var statePR struct {
		Session any `json:"session"`
	}
	if err := json.Unmarshal(statePRResp.Body.Bytes(), &statePR); err != nil {
		t.Fatalf("decode state/pr response: %v", err)
	}
	if statePR.Session != nil {
		t.Fatalf("expected nil session for unmatched pr lookup, got %#v", statePR.Session)
	}
}
