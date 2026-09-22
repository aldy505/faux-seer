package handler

import (
	"fmt"
	"net/http"
)

// seerModels reports the LLM models faux-seer actively routes requests to.
// Sentry's SeerModelsEndpoint parses this as {"models": [...]} and caches it for
// ten minutes.
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
	s.writeJSON(w, http.StatusOK, map[string]string{"content": "", "model": ""})
}

// serviceMapUpdate acknowledges an explorer service-map snapshot. Sentry only
// checks the response status, and faux-seer keeps no service map.
func (s *Server) serviceMapUpdate(w http.ResponseWriter, r *http.Request, body []byte) {
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// breakpointDetector answers Seer's breakpoint detection request. Sentry reads
// the `data` list of detected breakpoints; faux-seer detects none, which Sentry
// handles as a valid empty result.
func (s *Server) breakpointDetector(w http.ResponseWriter, r *http.Request, body []byte) {
	var request map[string]any
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode breakpoint detection request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
}
