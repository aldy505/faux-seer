package handler

import (
	"fmt"
	"net/http"
)

// codeReviewRerun handles POST /v1/code_review/check/rerun.
//
// Consumer: src/sentry/seer/code_review/webhooks/task.py:82 —
// process_github_webhook_event calls make_seer_request and discards the
// return value. Only the HTTP status matters; the body is never parsed.
func (s *Server) codeReviewRerun(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode code review rerun request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// codeReviewRequest handles POST /v1/code_review/review-request.
//
// Consumer: src/sentry/seer/code_review/webhooks/task.py:82 —
// process_github_webhook_event calls make_seer_request and discards the
// return value. Only the HTTP status matters; the body is never parsed.
func (s *Server) codeReviewRequest(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode code review request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// codeReviewPRClosed handles POST /v1/code_review/pr-closed.
//
// Consumer: src/sentry/seer/code_review/webhooks/task.py:82 —
// process_github_webhook_event calls make_seer_request and discards the
// return value. Only the HTTP status matters; the body is never parsed.
func (s *Server) codeReviewPRClosed(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode code review pr-closed request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// offboardRepository handles POST /v1/offboarding/repository.
//
// Consumer: src/sentry/deletions/tasks/seer.py:61 —
// notify_seer_repository_deleted calls make_seer_request and discards the
// return value. Only the HTTP status matters; the body is never parsed.
func (s *Server) offboardRepository(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode offboard repository request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
