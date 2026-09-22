package handler

import (
	"fmt"
	"net/http"
)

// seerModels reports the LLM models faux-seer actively routes requests to.
// Consumer: seer/models.py — parses {"models": [...]} and caches it for
// ten minutes. The list is driven by config; no LLM call is needed.
func (s *Server) seerModels(w http.ResponseWriter, _ *http.Request, _ []byte) {
	models := s.cfg.LLMModel
	if models == nil {
		models = []string{}
	}
	s.writeJSON(w, http.StatusOK, map[string][]string{"models": models})
}

// llmGenerate responds to a generic LLM generation request. Consumers read
// "content" (string) from the response:
//   - external_issues.py:98   — data.get("content"), then json.loads(content)
//   - issue_view_title_generate.py:74 — data.get("content")
//   - autofix_issue_data.py:200 — data.get("content") + data.get("model")
//   - seer_assertions.py:365  — data.get("content"), then json.loads(content)
//
// An empty "content" string is safe: callers that parse it handle JSONDecodeError;
// callers that check truthiness treat "" as falsy and fall back.
func (s *Server) llmGenerate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.generation.Generate(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// serviceMapUpdate acknowledges an explorer service-map snapshot.
//
// SIMULATED: faux-seer stores no service map. The request carries
// organization_id, nodes, and edges that are discarded; Sentry only checks
// the HTTP status.
func (s *Server) serviceMapUpdate(w http.ResponseWriter, r *http.Request, body []byte) {
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// breakpointDetector answers Seer's breakpoint detection request. Sentry reads
// the `data` list of detected breakpoints; faux-seer detects none, which Sentry
// handles as a valid empty result.
//
// SIMULATED: breakpoint detection requires a statistical timeseries backend
// not available in faux-seer.
func (s *Server) breakpointDetector(w http.ResponseWriter, r *http.Request, body []byte) {
	var request map[string]any
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode breakpoint detection request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
}
