package handler

import (
	"fmt"
	"net/http"
)

// prMetricsDelegatedAgentMatch handles POST /v1/pr-metrics/delegated-agent-match.
//
// Consumer: src/sentry/pr_metrics/webhooks.py:1356,1378 —
// _send_seer_delegated_agent_match reads the response body on HTTP 200.
// It calls DelegatedAgentMatch.validate(orjson.loads(response.data)) and reads:
//   - run_id   (int)   — the autofix run that produced the matching PR
//   - agent_id (string) — the coding agent identifier
//   - signal_type (string) — attribution signal type
//   - match_path (string) — resolved match path
//
// On HTTP 202 the consumer treats it as "no match found" and discards the body.
// On any other status the consumer logs and returns without reading the body.
//
// Source: src/sentry/seer/autofix/utils.py:249-256 — DelegatedAgentMatch model.
func (s *Server) prMetricsDelegatedAgentMatch(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode delegated-agent-match request: %v", err))
		return
	}

	// Return 202 — no match found. The consumer
	// (_send_seer_delegated_agent_match, webhooks.py:1373) treats 202 as
	// "sent, no sync match" and discards the body. Returning 200 with a
	// DelegatedAgentMatch payload would link the PR to a run, which
	// requires real state we don't have.
	s.writeJSON(w, http.StatusAccepted, map[string]bool{"success": true})
}

// prMetricsPRCloseJudge handles POST /v1/pr-metrics/pr-close-judge.
//
// Consumer: src/sentry/pr_metrics/judge.py:226-238 —
// forward_pr_to_seer_judge calls make_signed_seer_api_request and checks only
// the HTTP status code. The response body is never read; the actual judge
// verdict arrives later via the callback path (update_pr_metrics).
//
// A 2xx is sufficient to prevent the retry loop from firing.
func (s *Server) prMetricsPRCloseJudge(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode pr-close-judge request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
