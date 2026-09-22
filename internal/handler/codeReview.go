package handler

import (
	"fmt"
	"net/http"
	"strings"
)

// SIMULATED: faux-seer has no code-review agent. All three endpoints acknowledge
// the request so Sentry's webhook dispatch loop does not retry. The consumer
// (src/sentry/seer/code_review/webhooks/task.py:82) discards the body and
// checks only the HTTP status.

// codeReviewRerun handles POST /v1/code_review/check/rerun.
//
// SIMULATED: A full code-review agent is out of scope for faux-seer. The
// consumer (process_github_webhook_event, task.py:82) calls make_seer_request
// and discards the return value; only the HTTP status matters.
func (s *Server) codeReviewRerun(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode code review rerun request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// codeReviewRequest handles POST /v1/code_review/review-request.
//
// SIMULATED: A full code-review agent is out of scope for faux-seer. The
// consumer (process_github_webhook_event, task.py:82) calls make_seer_request
// and discards the return value; only the HTTP status matters.
func (s *Server) codeReviewRequest(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode code review request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// codeReviewPRClosed handles POST /v1/code_review/pr-closed.
//
// SIMULATED: A full code-review agent is out of scope for faux-seer. The
// consumer (process_github_webhook_event, task.py:82) calls make_seer_request
// and discards the return value; only the HTTP status matters.
func (s *Server) codeReviewPRClosed(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode code review pr-closed request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// offboardRepository handles POST /v1/offboarding/repository.
//
// Consumer: src/sentry/deletions/tasks/seer.py:33-62 —
// notify_seer_repository_deleted sends:
//
//	{
//	  "organization_id": int,
//	  "repository_id":   int,
//	  "provider":        str | None,
//	  "repository_name": str,
//	}
//
// and discards the return value (only the HTTP status matters).
//
// faux-seer drops the repository's code index so that stale chunks from the
// deleted repository are no longer surfaced in explorer searches. The
// organization_id is not used for code index lookups; the provider, owner, and
// name are derived from the repository_name field (format "owner/name").
func (s *Server) offboardRepository(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OrganizationID int64  `json:"organization_id"`
		RepositoryID   int64  `json:"repository_id"`
		Provider       string `json:"provider"`
		RepositoryName string `json:"repository_name"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode offboard repository request: %v", err))
		return
	}

	// Parse owner/name from repository_name. If the format is unexpected,
	// use empty strings — the code index will simply find nothing to delete.
	provider := request.Provider
	owner := ""
	name := request.RepositoryName
	if idx := strings.Index(name, "/"); idx >= 0 {
		owner = name[:idx]
		name = name[idx+1:]
	}

	// Drop the repository's code index. A nil codeIndex is safe — DeleteRepo
	// on a nil service is a no-op.
	if s.codeIndex != nil {
		if err := s.codeIndex.DeleteRepo(requestContext(r), request.OrganizationID, provider, owner, name); err != nil {
			// Log but still return success — Sentry ignores the body and
			// does not retry on failure.
			s.log.Error("offboard repository: delete code index", "error", err,
				"provider", provider, "owner", owner, "name", name)
		}
	}

	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
