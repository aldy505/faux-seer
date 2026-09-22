package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aldy505/faux-seer/internal/auth"
)

// seerRoute is one path Sentry's Seer client can call.
//
// wantStatus is the exact status faux-seer must answer, or 0 when any 2xx is
// acceptable. Every entry must at minimum avoid 404 (missing route, renamed
// path, or wrong HTTP method) and 5xx.
type seerRoute struct {
	name       string
	method     string
	path       string
	body       string
	wantStatus int
}

// seerRoutes is the full compatibility surface: every path discoverable in
// Sentry's Seer client layer. Keep it in sync with Server.Routes().
var seerRoutes = []seerRoute{
	// Agent and explorer runs.
	{name: "agent feature run", method: http.MethodPost, path: "/v1/automation/agent/feature/run", body: `{"feature_id":"x"}`},
	{name: "explorer chat", method: http.MethodPost, path: "/v1/automation/explorer/chat", body: `{"organization_id":1,"query":"why"}`},
	{name: "explorer state", method: http.MethodPost, path: "/v1/automation/explorer/state", body: `{"organization_id":1,"run_id":1}`},
	{name: "explorer state pr", method: http.MethodPost, path: "/v1/automation/explorer/state/pr", body: `{"organization_id":1,"provider":"github","pr_id":1}`},
	{name: "explorer runs by ids", method: http.MethodPost, path: "/v1/automation/explorer/runs/by-ids", body: `{"run_ids":[1]}`},
	{name: "explorer repos", method: http.MethodPost, path: "/v1/automation/explorer/repos", body: `{"run_id":1,"organization_id":1}`},
	{name: "explorer update", method: http.MethodPost, path: "/v1/automation/explorer/update", body: `{"organization_id":1,"run_id":1,"payload":{"type":"interrupt"}}`},
	{name: "explorer index", method: http.MethodPost, path: "/v1/automation/explorer/index", body: `{"org_id":1}`},
	{name: "explorer index org repo", method: http.MethodPost, path: "/v1/automation/explorer/index/org-repo-knowledge", body: `{"org_id":1,"repos":[]}`},
	{name: "explorer index org project", method: http.MethodPost, path: "/v1/automation/explorer/index/org-project-knowledge", body: `{"org_id":1}`},
	{name: "explorer index sentry knowledge", method: http.MethodPost, path: "/v1/automation/explorer/index/sentry-knowledge", body: `{"replace_existing":true}`},
	{name: "explorer export indexes", method: http.MethodPost, path: "/v1/automation/explorer/export-indexes", body: `{"org_id":1}`},
	{name: "service map update", method: http.MethodPost, path: "/v1/explorer/service-map/update", body: `{}`},

	// Autofix coding-agent state.
	{name: "coding agent state set", method: http.MethodPost, path: "/v1/automation/autofix/coding-agent/state/set", body: `{"run_id":1,"coding_agent_states":[]}`},
	{name: "coding agent state update", method: http.MethodPost, path: "/v1/automation/autofix/coding-agent/state/update", body: `{"agent_id":"a","updates":{}}`},

	// Summaries.
	{name: "summarize issue", method: http.MethodPost, path: "/v1/automation/summarize/issue", body: `{"group_id":"1","issue":{"title":"T"}}`},
	{name: "summarize trace", method: http.MethodPost, path: "/v1/automation/summarize/trace", body: `{"trace_id":"abc"}`},
	{name: "summarize fixability", method: http.MethodPost, path: "/v1/automation/summarize/fixability", body: `{"group_id":"1"}`},
	{name: "feedback spam detection", method: http.MethodPost, path: "/v1/automation/summarize/feedback/spam-detection", body: `{"organization_id":1,"feedback_message":"hi"}`},
	{name: "feedback labels", method: http.MethodPost, path: "/v1/automation/summarize/feedback/labels", body: `{"organization_id":1,"feedback_message":"hi"}`},
	{name: "feedback title", method: http.MethodPost, path: "/v1/automation/summarize/feedback/title", body: `{"organization_id":1,"feedback_message":"hi"}`},
	{name: "feedback label groups", method: http.MethodPost, path: "/v1/automation/summarize/feedback/label-groups", body: `{"labels":["a"]}`},
	{name: "feedback summarize", method: http.MethodPost, path: "/v1/automation/summarize/feedback/summarize", body: `{"feedbacks":["hi"]}`},
	{name: "replay breadcrumbs start", method: http.MethodPost, path: "/v1/automation/summarize/replay/breadcrumbs/start", body: `{"replay_id":"r1","num_segments":3}`},
	{name: "replay breadcrumbs state", method: http.MethodPost, path: "/v1/automation/summarize/replay/breadcrumbs/state", body: `{"replay_id":"r1"}`},
	{name: "replay breadcrumbs delete", method: http.MethodPost, path: "/v1/automation/summarize/replay/breadcrumbs/delete", body: `{"replay_ids":["r1"]}`},

	// Investigations.
	{name: "investigation create", method: http.MethodPost, path: "/v1/automation/investigations", body: `{"requestId":"3bed27f2-9ab9-49ce-8d64-7d78d5c3fd76"}`},
	{name: "investigation command", method: http.MethodPost, path: "/v1/automation/investigations/7/commands", body: `{"requestId":"3bed27f2-9ab9-49ce-8d64-7d78d5c3fd76","expectedWorkflowVersion":2}`},
	{name: "investigation get", method: http.MethodGet, path: "/v1/automation/investigations/7"},

	// Issue detection.
	{name: "issue detection analyze", method: http.MethodPost, path: "/v1/automation/issue-detection/analyze", body: `{"organization_id":1,"plan_tier":"business"}`, wantStatus: http.StatusAccepted},
	{name: "issue detection budget", method: http.MethodGet, path: "/v1/automation/issue-detection/check-budget/1"},

	// Codegen and assisted query.
	{name: "codegen unit tests", method: http.MethodPost, path: "/v1/automation/codegen/unit-tests", body: `{}`},
	{name: "assisted query state", method: http.MethodPost, path: "/v1/assisted-query/state", body: `{}`},
	{name: "assisted query start", method: http.MethodPost, path: "/v1/assisted-query/start", body: `{}`},
	{name: "assisted query translate", method: http.MethodPost, path: "/v1/assisted-query/translate", body: `{}`},
	{name: "assisted query translate agentic", method: http.MethodPost, path: "/v1/assisted-query/translate-agentic", body: `{}`},
	{name: "assisted query create cache", method: http.MethodPost, path: "/v1/assisted-query/create-cache", body: `{}`},

	// Anomaly detection, breakpoints, workflows.
	{name: "anomaly detect", method: http.MethodPost, path: "/v1/anomaly-detection/detect", body: `{}`},
	{name: "anomaly alert data", method: http.MethodPost, path: "/v1/anomaly-detection/alert-data", body: `{}`},
	{name: "anomaly store", method: http.MethodPost, path: "/v1/anomaly-detection/store", body: `{}`},
	{name: "anomaly delete alert data", method: http.MethodPost, path: "/v1/anomaly-detection/delete-alert-data", body: `{}`},
	{name: "workflow compare cohort", method: http.MethodPost, path: "/v1/workflows/compare/cohort", body: `{}`},
	{name: "breakpoint detector", method: http.MethodPost, path: "/trends/breakpoint-detector", body: `{}`},

	// Code review, offboarding, PR metrics.
	{name: "code review rerun", method: http.MethodPost, path: "/v1/code_review/check/rerun", body: `{}`},
	{name: "code review request", method: http.MethodPost, path: "/v1/code_review/review-request", body: `{}`},
	{name: "code review pr closed", method: http.MethodPost, path: "/v1/code_review/pr-closed", body: `{}`},
	{name: "offboard repository", method: http.MethodPost, path: "/v1/offboarding/repository", body: `{"organization_id":1,"repository_id":2}`},
	{name: "pr metrics delegated agent match", method: http.MethodPost, path: "/v1/pr-metrics/delegated-agent-match", body: `{}`},
	{name: "pr metrics pr close judge", method: http.MethodPost, path: "/v1/pr-metrics/pr-close-judge", body: `{}`},

	// Grouping, supergroups, severity, models, monitoring providers.
	{name: "similar issues", method: http.MethodPost, path: "/v0/issues/similar-issues", body: `{"project_id":1,"stacktrace":"x","hash":"h"}`},
	{name: "grouping delete project", method: http.MethodGet, path: "/v0/issues/similar-issues/grouping-record/delete/1"},
	{name: "grouping delete by hash", method: http.MethodPost, path: "/v0/issues/similar-issues/grouping-record/delete-by-hash", body: `{"project_id":1,"hash_list":["h"]}`},
	{name: "supergroups cluster lightweight", method: http.MethodPost, path: "/v0/issues/supergroups/cluster-lightweight", body: `{"organization_id":1,"group_id":1}`},
	{name: "supergroups get", method: http.MethodPost, path: "/v0/issues/supergroups/get", body: `{"organization_id":1}`},
	{name: "supergroups get by group ids", method: http.MethodPost, path: "/v0/issues/supergroups/get-by-group-ids", body: `{"organization_id":1,"group_ids":[1]}`},
	{name: "severity score", method: http.MethodPost, path: "/v0/issues/severity-score", body: `{"message":"panic","has_stacktrace":1}`},
	{name: "seer models", method: http.MethodGet, path: "/v1/models"},
	{name: "llm generate", method: http.MethodPost, path: "/v1/llm/generate", body: `{"referrer":"x"}`},
	{name: "oneshot run", method: http.MethodPost, path: "/v1/automation/oneshot/run", body: `{}`},
	{name: "monitoring provider verify", method: http.MethodPost, path: "/v1/monitoring-providers/gcp/verify-connection", body: `{"gcp_project_ids":["p"]}`},

	// Project preference maintenance.
	{name: "preference remove repository", method: http.MethodPost, path: "/v1/project-preference/remove-repository", body: `{"organization_id":1}`},
	{name: "preference bulk remove repositories", method: http.MethodPost, path: "/v1/project-preference/bulk-remove-repositories", body: `{"organization_id":1}`},
	{name: "preference remove handoffs", method: http.MethodPost, path: "/v1/project-preference/remove-handoffs-for-integration", body: `{"organization_id":1}`},
}

func TestSeerRouteSurface(t *testing.T) {
	server := newTestServer(t)

	for _, route := range seerRoutes {
		t.Run(route.name, func(t *testing.T) {
			payload := []byte(route.body)
			req := httptest.NewRequest(route.method, route.path, bytes.NewReader(payload))
			req.Header.Set("Authorization", auth.SignRequestBody(payload, "test-secret"))
			resp := httptest.NewRecorder()
			server.Routes().ServeHTTP(resp, req)

			if resp.Code == http.StatusNotFound {
				t.Fatalf("%s %s is not routed (got 404)", route.method, route.path)
			}
			if resp.Code >= 500 {
				t.Fatalf("%s %s returned %d: %s", route.method, route.path, resp.Code, resp.Body.String())
			}
			if route.wantStatus != 0 && resp.Code != route.wantStatus {
				t.Fatalf("%s %s returned %d, want %d: %s", route.method, route.path, resp.Code, route.wantStatus, resp.Body.String())
			}
		})
	}
}

// TestRemovedSeerRoutesAreNotServed pins the paths that appear nowhere in
// Sentry's Seer client layer, so they are not accidentally reintroduced.
func TestRemovedSeerRoutesAreNotServed(t *testing.T) {
	server := newTestServer(t)
	removed := []string{
		"/v1/automation/autofix/start",
		"/v1/automation/autofix/update",
		"/v1/automation/autofix/state",
		"/v1/automation/autofix/state/pr",
		"/v1/automation/autofix/prompt",
		"/v1/automation/codebase/repo/check-access",
		"/v1/issues/severity-score",
		"/v1/project-preference",
		"/v1/project-preference/set",
		"/v1/project-preference/bulk",
		"/v1/project-preference/bulk-set",
		"/v0/issues/similar-issues/grouping-record",
		"/v0/issues/supergroups",
		"/v0/issues/supergroups/list",
		"/v1/automation/explorer/runs",
	}

	for _, path := range removed {
		t.Run(path, func(t *testing.T) {
			payload := []byte(`{}`)
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
			req.Header.Set("Authorization", auth.SignRequestBody(payload, "test-secret"))
			resp := httptest.NewRecorder()
			server.Routes().ServeHTTP(resp, req)

			if resp.Code != http.StatusNotFound {
				t.Fatalf("POST %s returned %d, want 404", path, resp.Code)
			}
		})
	}
}
