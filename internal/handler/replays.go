package handler

import (
	"fmt"
	"net/http"
	"time"
)

// replayBreadcrumbsStart begins breadcrumb summarization. faux-seer completes
// synchronously and returns the full summary immediately.
func (s *Server) replayBreadcrumbsStart(w http.ResponseWriter, r *http.Request, body []byte) {
	s.replayBreadcrumbsSummary(w, body)
}

// replayBreadcrumbsState polls for the result of a breadcrumb summarization
// run. faux-seer completed synchronously, so it returns the same payload.
func (s *Server) replayBreadcrumbsState(w http.ResponseWriter, r *http.Request, body []byte) {
	s.replayBreadcrumbsSummary(w, body)
}

// replayBreadcrumbsSummary returns the breadcrumb summary response shared by
// both start and state handlers so the shapes cannot drift.
func (s *Server) replayBreadcrumbsSummary(w http.ResponseWriter, body []byte) {
	var request struct {
		NumSegments *int64 `json:"num_segments"`
	}
	// Tolerate empty body or wrong-typed fields; we only need the optional
	// num_segments and never use replay_id.
	_ = decodeOptionalJSONBody(body, &request)

	s.writeJSON(w, http.StatusOK, map[string]any{
		"created_at":   time.Now().UTC().Format(time.RFC3339),
		"status":       "completed",
		"num_segments": request.NumSegments,
		"data": map[string]any{
			"summary":     "Breadcrumbs summarized.",
			"time_ranges": []any{},
		},
	})
}

// replayBreadcrumbsDelete acknowledges deletion of replay breadcrumb data.
// Sentry checks only the HTTP status code.
func (s *Server) replayBreadcrumbsDelete(w http.ResponseWriter, r *http.Request, body []byte) {
	// Parse for validation tolerance; we don't use the IDs.
	var request struct {
		ReplayIDs []string `json:"replay_ids"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode replay breadcrumbs delete request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
