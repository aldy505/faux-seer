package handler

import (
	"fmt"
	"net/http"

	"github.com/aldy505/faux-seer/internal/monitoring"
)

// monitoringProviderVerifyConnection answers Seer's GCP connection verification.
//
// Consumer: src/sentry/integrations/gcp/client.py:145-170 —
// verify_gcp_connection reads connection_status, projects, and error_detail
// from the response. The DRF serializer in
// src/sentry/api/endpoints/organization_monitoring_provider_verify_connection.py
// validates connection_status is present and each project has gcp_project_id
// and connection_status.
func (s *Server) monitoringProviderVerifyConnection(w http.ResponseWriter, r *http.Request, body []byte) {
	var request monitoring.VerifyRequest
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode monitoring provider verify-connection request: %v", err))
		return
	}

	result, err := s.monitoring.VerifyConnection(requestContext(r), request)
	if err != nil {
		// Verification outcomes belong in the body: Sentry reads
		// connection_status and error_detail from a 200 response.
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("verify GCP connection: %v", err))
		return
	}

	s.writeJSON(w, http.StatusOK, result)
}
