package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aldy505/faux-seer/internal/db"
)

// DelegatedAgentMatch is the response shape Sentry validates for a synchronous
// match. See src/sentry/seer/autofix/utils.py:249-256.
type DelegatedAgentMatch struct {
	RunID      int64  `json:"run_id"`
	AgentID    string `json:"agent_id"`
	SignalType string `json:"signal_type"`
	MatchPath  string `json:"match_path"`
}

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
	var request struct {
		OrganizationID int64   `json:"organization_id"`
		PullRequestID  int64   `json:"pull_request_id"`
		PRURL          string  `json:"pr_url"`
		Provider       string  `json:"provider"`
		HeadBranch     string  `json:"head_branch"`
		GroupIDs       []int64 `json:"group_ids"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode delegated-agent-match request: %v", err))
		return
	}

	// Try a direct provider+PR match first.
	if request.Provider != "" && request.PullRequestID > 0 {
		record, err := s.store.GetAutofixRunByPR(requestContext(r), request.Provider, request.PullRequestID)
		if err == nil && record != nil {
			match := extractDelegatedMatch(record)
			if match != nil {
				s.writeJSON(w, http.StatusOK, match)
				return
			}
		}
	}

	// Fall back to searching for a run whose state blob references the PR URL.
	if request.PRURL != "" {
		record, err := s.store.FindAutofixRunByPRURL(requestContext(r), request.PRURL)
		if err == nil && record != nil {
			match := extractDelegatedMatch(record)
			if match != nil {
				s.writeJSON(w, http.StatusOK, match)
				return
			}
		}
	}

	// Return 202 — no match found. The consumer
	// (_send_seer_delegated_agent_match, webhooks.py:1373) treats 202 as
	// "sent, no sync match" and discards the body. Returning 200 with a
	// DelegatedAgentMatch payload would link the PR to a run, which
	// requires real state we don't have.
	s.writeJSON(w, http.StatusAccepted, map[string]bool{"success": true})
}

// signalTypes are the values Sentry's PullRequestAttributionSignalType accepts
// (src/sentry/models/pullrequest.py:63). Sentry runs the reported signal_type
// through that enum and discards the whole match when the value is unknown, so
// a match must never carry an invented signal type.
const (
	signalTypeCursor        = "seer_delegated:cursor"
	signalTypeGitHubCopilot = "seer_delegated:github_copilot"
	signalTypeClaudeCode    = "seer_delegated:claude_code"
	signalTypeUnknown       = "seer_delegated:unknown"
)

// extractDelegatedMatch builds a DelegatedAgentMatch from an autofix run
// record. It picks the coding agent that produced the run's PR and derives the
// attribution signal type from that agent's provider.
func extractDelegatedMatch(record *db.AutofixRunRecord) *DelegatedAgentMatch {
	if record == nil {
		return nil
	}
	state := rawMapState(record.StateJSON)
	codingAgents, _ := state["coding_agents"].(map[string]any)
	if len(codingAgents) == 0 {
		return nil
	}
	// Pick the first agent id — the consumer expects exactly one match.
	for agentID, agentData := range codingAgents {
		agentMap, _ := agentData.(map[string]any)
		return &DelegatedAgentMatch{
			RunID:      record.ID,
			AgentID:    agentID,
			SignalType: signalTypeForAgent(agentMap),
			MatchPath:  "coding_agent_state",
		}
	}
	return nil
}

// signalTypeForAgent maps a coding agent's provider onto Sentry's attribution
// enum, falling back to the unknown member.
func signalTypeForAgent(agent map[string]any) string {
	identity := strings.ToLower(strings.Join([]string{
		stringValue(agent, "provider"),
		stringValue(agent, "name"),
	}, " "))
	switch {
	case strings.Contains(identity, "cursor"):
		return signalTypeCursor
	case strings.Contains(identity, "copilot"):
		return signalTypeGitHubCopilot
	case strings.Contains(identity, "claude"):
		return signalTypeClaudeCode
	default:
		return signalTypeUnknown
	}
}

func stringValue(source map[string]any, key string) string {
	value, _ := source[key].(string)
	return value
}

// rawMapState deserializes a JSON state blob into a map.
func rawMapState(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
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
