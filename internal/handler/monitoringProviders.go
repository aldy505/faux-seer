package handler

import (
	"fmt"
	"net/http"
)

// monitoringProviderVerifyConnection answers Seer's GCP connection verification.
//
// faux-seer performs no GCP verification and holds no customer service-account
// credentials, so it reports a simulated successful connection. Sentry's GCP
// integration setup persists this result, so the simulation is deliberate: a
// compatibility layer that always failed verification would block the setup flow.
func (s *Server) monitoringProviderVerifyConnection(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		SentryServiceAccountEmail   string   `json:"sentry_sa_email"`
		CustomerServiceAccountEmail string   `json:"customer_sa_email"`
		GCPProjectIDs               []string `json:"gcp_project_ids"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode monitoring provider verify-connection request: %v", err))
		return
	}
	projects := make([]map[string]any, 0, len(request.GCPProjectIDs))
	for _, projectID := range request.GCPProjectIDs {
		projects = append(projects, map[string]any{
			"gcp_project_id":    projectID,
			"connection_status": "connected",
			"services":          []any{},
			"error_detail":      nil,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"connection_status": "connected",
		"projects":          projects,
		"error_detail":      nil,
	})
}
