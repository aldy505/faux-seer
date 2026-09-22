package handler

import (
	"fmt"
	"net/http"
)

// projectPreferenceRemoveRepository acknowledges Seer's repository-removal
// request. faux-seer stores no project preferences, so there is nothing to
// remove and success is reported immediately.
func (s *Server) projectPreferenceRemoveRepository(w http.ResponseWriter, r *http.Request, body []byte) {
	s.projectPreferenceAck(w, body, "remove repository")
}

// projectPreferenceBulkRemoveRepositories acknowledges a bulk repository removal.
func (s *Server) projectPreferenceBulkRemoveRepositories(w http.ResponseWriter, r *http.Request, body []byte) {
	s.projectPreferenceAck(w, body, "bulk remove repositories")
}

// projectPreferenceRemoveHandoffsForIntegration acknowledges handoff removal.
func (s *Server) projectPreferenceRemoveHandoffsForIntegration(w http.ResponseWriter, r *http.Request, body []byte) {
	s.projectPreferenceAck(w, body, "remove handoffs for integration")
}

// projectPreferenceAck validates the request body and reports success.
func (s *Server) projectPreferenceAck(w http.ResponseWriter, body []byte, name string) {
	var request map[string]any
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode %s request: %v", name, err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
