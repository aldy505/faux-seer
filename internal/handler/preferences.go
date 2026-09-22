package handler

import (
	"fmt"
	"net/http"
)

// projectPreferenceRemoveRepository drops one repository from the
// organization's stored Seer preferences. Sentry reads only `success`, so the
// call stays idempotent: removing a repository twice reports success twice.
func (s *Server) projectPreferenceRemoveRepository(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := s.preferences.RemoveRepository(requestContext(r), body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("remove repository: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// projectPreferenceBulkRemoveRepositories drops several repositories at once.
func (s *Server) projectPreferenceBulkRemoveRepositories(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := s.preferences.BulkRemoveRepositories(requestContext(r), body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("bulk remove repositories: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// projectPreferenceRemoveHandoffsForIntegration clears the stored integration
// handoff for the organization.
func (s *Server) projectPreferenceRemoveHandoffsForIntegration(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := s.preferences.RemoveHandoffsForIntegration(requestContext(r), body); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("remove handoffs for integration: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
