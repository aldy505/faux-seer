package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"uuid"
)

// investigationCreate handles POST /v1/automation/investigations.
func (s *Server) investigationCreate(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode investigation create request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"runId":      1,
		"created":    true,
		"projection": map[string]any{},
	})
}

// investigationCommand handles POST /v1/automation/investigations/{run_id}/commands.
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

	workflowVersion := int64(1)
	if request.ExpectedWorkflowVersion >= 1 {
		workflowVersion = request.ExpectedWorkflowVersion + 1
	}

	s.writeJSON(w, http.StatusOK, map[string]any{
		"runId":           runID,
		"requestId":       requestID,
		"accepted":        true,
		"duplicate":       false,
		"workflowVersion": workflowVersion,
		"projection":      map[string]any{},
	})
}

// investigationGet handles GET /v1/automation/investigations/{run_id}.
func (s *Server) investigationGet(w http.ResponseWriter, r *http.Request, body []byte) {
	runID := parseRunID(r.PathValue("run_id"))
	s.writeJSON(w, http.StatusOK, map[string]any{
		"runId":      runID,
		"created":    true,
		"projection": map[string]any{},
	})
}

// issueDetectionAnalyze handles POST /v1/automation/issue-detection/analyze.
// Sentry accepts this endpoint only when the status is exactly 202.
func (s *Server) issueDetectionAnalyze(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode issue detection analyze request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]bool{"success": true})
}

// issueDetectionCheckBudget handles GET /v1/automation/issue-detection/check-budget/{org_id}.
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
