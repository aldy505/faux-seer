package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"uuid"
)

// These tests pin the response contracts that Sentry validates strictly or
// indexes into. A renamed or missing key here breaks a live Sentry caller
// silently, so each assertion corresponds to a specific consumer requirement.

// decodeBody unmarshals a recorded response body, failing the test on bad JSON.
func decodeBody(t *testing.T, resp *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response body %s: %v", resp.Body.String(), err)
	}
}

// TestModelsEndpointReportsConfiguredModels covers Sentry's SeerModelsEndpoint,
// which parses {"models": [str]} and caches it for ten minutes.
func TestModelsEndpointReportsConfiguredModels(t *testing.T) {
	server := newTestServer(t)
	resp := issueRequest(server, http.MethodGet, "/v1/models", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Models []string `json:"models"`
	}
	decodeBody(t, resp, &body)
	if len(body.Models) != 1 || body.Models[0] != "test-model" {
		t.Fatalf("expected models [test-model], got %#v", body.Models)
	}
}

// TestExplorerStateMatchesSeerRunStateShape covers Sentry's SeerRunState
// pydantic model, which raises ValidationError when any required field is
// missing: run_id, blocks (each with id, message.role, timestamp), status
// (a fixed literal set), and updated_at.
func TestExplorerStateMatchesSeerRunStateShape(t *testing.T) {
	server := newTestServer(t)
	_ = issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat",
		[]byte(`{"organization_id":1,"query":"why","user_org_context":{"user_id":7}}`))

	resp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/state", []byte(`{"organization_id":1,"run_id":1}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Session *struct {
			RunID     int64  `json:"run_id"`
			Status    string `json:"status"`
			UpdatedAt string `json:"updated_at"`
			Blocks    []struct {
				ID      string `json:"id"`
				Message struct {
					Role string `json:"role"`
				} `json:"message"`
				Timestamp string `json:"timestamp"`
			} `json:"blocks"`
		} `json:"session"`
	}
	decodeBody(t, resp, &body)

	if body.Session == nil {
		t.Fatal("expected a session object")
	}
	if body.Session.RunID == 0 {
		t.Fatalf("expected non-zero run_id, got %#v", body.Session)
	}
	if body.Session.UpdatedAt == "" {
		t.Fatal("expected non-empty updated_at")
	}
	switch body.Session.Status {
	case "processing", "completed", "error", "awaiting_user_input":
	default:
		t.Fatalf("status %q is outside SeerRunState's allowed values", body.Session.Status)
	}
	if len(body.Session.Blocks) == 0 {
		t.Fatal("expected at least one block")
	}
	for _, block := range body.Session.Blocks {
		if block.ID == "" || block.Timestamp == "" || block.Message.Role == "" {
			t.Fatalf("block is missing a required field: %#v", block)
		}
	}
}

// TestRunsByIDsReturnsStatusMapKeyedByRunID covers fetch_run_statuses, which
// iterates data["data"].items() and reads run["status"]. A list (rather than a
// dict) or a null would make Sentry's batch status poll silently return {}.
func TestRunsByIDsReturnsStatusMapKeyedByRunID(t *testing.T) {
	server := newTestServer(t)
	_ = issueRequest(server, http.MethodPost, "/v1/automation/explorer/chat", []byte(`{"organization_id":1,"query":"why"}`))

	resp := issueRequest(server, http.MethodPost, "/v1/automation/explorer/runs/by-ids", []byte(`{"run_ids":[1,987654]}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		Data map[string]struct {
			RunID  int64  `json:"run_id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	decodeBody(t, resp, &body)

	if body.Data == nil {
		t.Fatal("data must be a JSON object keyed by run id, not null")
	}
	if _, found := body.Data["987654"]; found {
		t.Fatalf("unknown run ids must be omitted, got %#v", body.Data)
	}
	entry, ok := body.Data["1"]
	if !ok {
		t.Fatalf("expected run 1 in %#v", body.Data)
	}
	if entry.Status == "" {
		t.Fatal("each run entry must carry a status string")
	}
}

// TestInvestigationCommandMatchesStrictModel covers Sentry's pydantic model
// for command responses: runId >= 1, a UUID requestId, accepted literally true,
// boolean duplicate, workflowVersion >= 1, and a projection dict. The strict
// types mean a wrong type raises and Sentry reports a 502.
func TestInvestigationCommandMatchesStrictModel(t *testing.T) {
	server := newTestServer(t)
	sentUUID := "3bed27f2-9ab9-49ce-8d64-7d78d5c3fd76"

	resp := issueRequest(server, http.MethodPost, "/v1/automation/investigations/7/commands",
		[]byte(`{"requestId":"`+sentUUID+`","expectedWorkflowVersion":2,"command":{"type":"x"}}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		RunID           int64          `json:"runId"`
		RequestID       string         `json:"requestId"`
		Accepted        bool           `json:"accepted"`
		Duplicate       bool           `json:"duplicate"`
		WorkflowVersion int64          `json:"workflowVersion"`
		Projection      map[string]any `json:"projection"`
	}
	decodeBody(t, resp, &body)

	if body.RunID < 1 {
		t.Fatalf("runId must be >= 1, got %d", body.RunID)
	}
	if body.WorkflowVersion < 1 {
		t.Fatalf("workflowVersion must be >= 1, got %d", body.WorkflowVersion)
	}
	if !body.Accepted {
		t.Fatal("accepted must be true; Sentry validates it as Literal[True]")
	}
	if body.Projection == nil {
		t.Fatal("projection must be a JSON object")
	}
	parsed, err := uuid.Parse(body.RequestID)
	if err != nil {
		t.Fatalf("requestId must be a UUID, got %q: %v", body.RequestID, err)
	}
	if parsed.String() != sentUUID {
		t.Fatalf("expected the request UUID to be echoed, got %q", body.RequestID)
	}

	// An unusable requestId must still yield a parseable UUID rather than an error.
	resp = issueRequest(server, http.MethodPost, "/v1/automation/investigations/7/commands", []byte(`{"requestId":"nope"}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200 for an invalid requestId, got %d: %s", resp.Code, resp.Body.String())
	}
	var fallback struct {
		RequestID string `json:"requestId"`
	}
	decodeBody(t, resp, &fallback)
	if _, err := uuid.Parse(fallback.RequestID); err != nil {
		t.Fatalf("expected a generated UUID, got %q: %v", fallback.RequestID, err)
	}
}

// TestReplayBreadcrumbsResponseMatchesFrontendShape covers the shape Sentry
// passes through to its React frontend, which stops polling only on the
// lowercase "completed"/"error" statuses and renders data.summary.
func TestReplayBreadcrumbsResponseMatchesFrontendShape(t *testing.T) {
	server := newTestServer(t)

	for _, path := range []string{
		"/v1/automation/summarize/replay/breadcrumbs/start",
		"/v1/automation/summarize/replay/breadcrumbs/state",
	} {
		resp := issueRequest(server, http.MethodPost, path, []byte(`{"replay_id":"r1","num_segments":5}`))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", path, resp.Code, resp.Body.String())
		}

		var body struct {
			Status      string  `json:"status"`
			CreatedAt   *string `json:"created_at"`
			NumSegments *int64  `json:"num_segments"`
			Data        *struct {
				Summary    string `json:"summary"`
				TimeRanges []any  `json:"time_ranges"`
			} `json:"data"`
		}
		decodeBody(t, resp, &body)

		switch body.Status {
		case "processing", "completed", "error", "not_started":
		default:
			t.Fatalf("%s: status %q is outside the frontend enum", path, body.Status)
		}
		if body.CreatedAt == nil || *body.CreatedAt == "" {
			t.Fatalf("%s: created_at must be a non-empty timestamp", path)
		}
		if body.Data == nil || body.Data.Summary == "" || body.Data.TimeRanges == nil {
			t.Fatalf("%s: data must carry summary and a time_ranges array, got %#v", path, body.Data)
		}
	}
}

// TestFeedbackResponseNestedShapes covers the nested keys Sentry indexes into
// directly: data.labels, each label group's primaryLabel/associatedLabels, and
// a real boolean is_spam.
func TestFeedbackResponseNestedShapes(t *testing.T) {
	server := newTestServer(t)

	resp := issueRequest(server, http.MethodPost, "/v1/automation/summarize/feedback/spam-detection", []byte(`{"feedback_message":"hi"}`))
	var spam struct {
		IsSpam *bool `json:"is_spam"`
	}
	decodeBody(t, resp, &spam)
	if spam.IsSpam == nil {
		t.Fatal("is_spam must be present and boolean")
	}

	resp = issueRequest(server, http.MethodPost, "/v1/automation/summarize/feedback/labels", []byte(`{"feedback_message":"hi"}`))
	var labels struct {
		Data *struct {
			Labels []string `json:"labels"`
		} `json:"data"`
	}
	decodeBody(t, resp, &labels)
	if labels.Data == nil || labels.Data.Labels == nil {
		t.Fatalf("data.labels must be a JSON array, got %#v", labels.Data)
	}

	resp = issueRequest(server, http.MethodPost, "/v1/automation/summarize/feedback/label-groups", []byte(`{"labels":["a","b"]}`))
	var groups struct {
		Data []struct {
			PrimaryLabel     string   `json:"primaryLabel"`
			AssociatedLabels []string `json:"associatedLabels"`
		} `json:"data"`
	}
	decodeBody(t, resp, &groups)
	if len(groups.Data) != 2 {
		t.Fatalf("expected one group per requested label, got %#v", groups.Data)
	}
	for _, group := range groups.Data {
		if group.PrimaryLabel == "" {
			t.Fatalf("primaryLabel is required, got %#v", group)
		}
		if group.AssociatedLabels == nil {
			t.Fatalf("associatedLabels must be a JSON array, got %#v", group)
		}
	}
}

// TestMonitoringProviderVerifyConnectionSerializerKeys covers Sentry's DRF
// serializer, which requires connection_status plus, per project,
// gcp_project_id, connection_status and services.
func TestMonitoringProviderVerifyConnectionSerializerKeys(t *testing.T) {
	server := newTestServer(t)
	resp := issueRequest(server, http.MethodPost, "/v1/monitoring-providers/gcp/verify-connection",
		[]byte(`{"sentry_sa_email":"a","customer_sa_email":"b","gcp_project_ids":["p1","p2"]}`))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}

	var body struct {
		ConnectionStatus string  `json:"connection_status"`
		ErrorDetail      *string `json:"error_detail"`
		Projects         []struct {
			GCPProjectID     string  `json:"gcp_project_id"`
			ConnectionStatus string  `json:"connection_status"`
			Services         []any   `json:"services"`
			ErrorDetail      *string `json:"error_detail"`
		} `json:"projects"`
	}
	decodeBody(t, resp, &body)

	if body.ConnectionStatus == "" {
		t.Fatal("connection_status is required by the serializer")
	}
	if len(body.Projects) != 2 {
		t.Fatalf("expected one entry per requested project id, got %#v", body.Projects)
	}
	for _, project := range body.Projects {
		if project.GCPProjectID == "" || project.ConnectionStatus == "" || project.Services == nil {
			t.Fatalf("project entry is missing a serializer-required field: %#v", project)
		}
	}
}

// TestListsThatSentryIteratesAreNeverNull guards the two collection responses
// Sentry iterates directly: a JSON null there raises instead of degrading.
func TestListsThatSentryIteratesAreNeverNull(t *testing.T) {
	server := newTestServer(t)

	resp := issueRequest(server, http.MethodPost, "/v0/issues/similar-issues", []byte(`{"project_id":1,"stacktrace":"x","hash":"h"}`))
	var similar struct {
		Responses []any `json:"responses"`
	}
	decodeBody(t, resp, &similar)
	if similar.Responses == nil {
		t.Fatal("similar-issues responses must be an array, not null")
	}

	for _, path := range []string{"/v0/issues/supergroups/get", "/v0/issues/supergroups/get-by-group-ids"} {
		resp := issueRequest(server, http.MethodPost, path, []byte(`{"organization_id":1,"group_ids":[1]}`))
		var supergroups struct {
			Data []any `json:"data"`
		}
		decodeBody(t, resp, &supergroups)
		if supergroups.Data == nil {
			t.Fatalf("%s: data must be an array, not null", path)
		}
	}
}
