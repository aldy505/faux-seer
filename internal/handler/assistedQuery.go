package handler

import (
	"fmt"
	"net/http"
	"time"
)

// assistedQueryStart acknowledges a search-agent start request. The Sentry
// outbox handler (receivers/outbox/cell.py:334) reads "run_id" from the
// response JSON and stores it on the SeerRun mirror. faux-seer returns a
// stable ID so downstream polling succeeds.
func (s *Server) assistedQueryStart(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assisted-query start request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]int64{"run_id": 1})
}

// assistedQueryState returns the current session state for a search-agent run.
// The Sentry consumer (seer/endpoints/search_agent_state.py:87) passes the
// full JSON body through to the frontend, which reads the "session" key.
func (s *Server) assistedQueryState(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		RunID          int64 `json:"run_id"`
		OrganizationID int64 `json:"organization_id"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assisted-query state request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"session": map[string]any{
			"run_id":             request.RunID,
			"status":             "completed",
			"current_step":       nil,
			"completed_steps":    []any{},
			"updated_at":         time.Now().UTC().Format(time.RFC3339),
			"final_response":     nil,
			"unsupported_reason": nil,
		},
	})
}

// assistedQueryTranslate translates a natural-language query into Sentry EQS
// queries. The Sentry consumer (seer/endpoints/trace_explorer_ai_query.py:127)
// reads "responses" and "unsupported_reason" from the Seer response, then
// restructures each entry into {query, stats_period, group_by, visualization,
// sort, mode}. faux-seer returns an empty response set so the frontend
// displays "no results".
func (s *Server) assistedQueryTranslate(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OrgID                int64   `json:"org_id"`
		ProjectIDs           []int64 `json:"project_ids"`
		NaturalLanguageQuery string  `json:"natural_language_query"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assisted-query translate request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"responses":          []any{},
		"unsupported_reason": nil,
	})
}

// assistedQueryTranslateAgentic translates a natural-language query using the
// agentic search strategy. The Sentry consumer
// (seer/endpoints/trace_explorer_ai_translate_agentic.py:148) passes the full
// Seer JSON response through to the frontend unchanged. faux-seer returns an
// empty responses list matching the translate contract.
func (s *Server) assistedQueryTranslateAgentic(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OrgID                int64   `json:"org_id"`
		OrgSlug              string  `json:"org_slug"`
		ProjectIDs           []int64 `json:"project_ids"`
		NaturalLanguageQuery string  `json:"natural_language_query"`
		Strategy             string  `json:"strategy"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assisted-query translate-agentic request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"responses":          []any{},
		"unsupported_reason": nil,
	})
}

// assistedQueryCreateCache acknowledges the trace-explorer setup request that
// primes cached prompts. The Sentry consumer
// (seer/endpoints/trace_explorer_ai_setup.py:28) only checks the HTTP status;
// the body is not inspected for keys.
func (s *Server) assistedQueryCreateCache(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assisted-query create-cache request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// unitTestsGenerate acknowledges a unit-test generation request. The Sentry
// consumer (seer/services/test_generation/impl.py:28) only checks
// response.status == 200; the body is never inspected.
func (s *Server) unitTestsGenerate(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode unit-test generation request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// oneshotRun dispatches a synchronous one-shot LLM task. The Sentry consumer
// (seer/oneshot.py:132) reads data["result"] from the response. Individual
// callers then extract oneshot-specific keys from the result dict
// (e.g. "title" from conversation_title, "answer" from agent_question).
// faux-seer returns an empty result envelope; callers handle missing keys
// gracefully.
func (s *Server) oneshotRun(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OneshotID string         `json:"oneshot_id"`
		Payload   map[string]any `json:"payload"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode oneshot run request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"result": map[string]any{}})
}
