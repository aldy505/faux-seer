package handler

import (
	"fmt"
	"net/http"
	"strconv"

	"uuid"
)

// investigationCreate handles POST /v1/automation/investigations.
//
// Sentry sends requestId, investigationId, source, activeTimeBudgetSeconds and
// optional monitoring providers, then validates the response against a model
// requiring runId >= 1, a boolean created, and a projection object.
func (s *Server) investigationCreate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.investigations.Create(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// investigationCommand handles POST /v1/automation/investigations/{run_id}/commands.
//
// The requestId is echoed back because Sentry parses it as a UUID, and the
// workflow version advances per accepted command. A repeated requestId is
// reported as duplicate instead of being applied twice.
func (s *Server) investigationCommand(w http.ResponseWriter, r *http.Request, body []byte) {
	runID := parseRunID(r.PathValue("run_id"))
	var request struct {
		RequestID               string `json:"requestId"`
		ExpectedWorkflowVersion int64  `json:"expectedWorkflowVersion"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode investigation command request: %v", err))
		return
	}
	requestID := request.RequestID
	if parsed, err := uuid.Parse(requestID); err == nil {
		requestID = parsed.String()
	} else {
		requestID = uuid.New().String()
	}
	response, err := s.investigations.Command(requestContext(r), runID, requestID, body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// investigationGet handles GET /v1/automation/investigations/{run_id}.
func (s *Server) investigationGet(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.investigations.Get(requestContext(r), parseRunID(r.PathValue("run_id")))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// issueDetectionAnalyze handles POST /v1/automation/issue-detection/analyze.
//
// SIMULATED: Sentry accepts this endpoint only when the status is exactly 202,
// and a real detection pass needs the event pipeline that feeds it, which is not
// reachable from faux-seer.
func (s *Server) issueDetectionAnalyze(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode issue detection analyze request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]bool{"success": true})
}

// issueDetectionCheckBudget handles GET /v1/automation/issue-detection/check-budget/{org_id}.
//
// SIMULATED: faux-seer keeps no detection budget, so it always reports that the
// organization has budget left for one more analysis.
func (s *Server) issueDetectionCheckBudget(w http.ResponseWriter, r *http.Request, body []byte) {
	s.writeJSON(w, http.StatusOK, map[string]bool{"has_budget": true})
}

// parseRunID extracts a run ID from the path, defaulting to 1 when the value
// is missing, non-numeric, or less than 1.
func parseRunID(raw string) int64 {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 {
		return 1
	}
	return n
}
