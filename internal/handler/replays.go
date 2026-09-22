package handler

import (
	"net/http"
)

// replayBreadcrumbsStart begins breadcrumb summarization. The replay service
// generates a summary via LLM and persists the result synchronously.
func (s *Server) replayBreadcrumbsStart(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.replay.Start(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// replayBreadcrumbsState polls for the result of a breadcrumb summarization
// run. Consumer: replays/endpoints/project_replay_summary.py — polls until
// status reaches "completed" or "error", then reads data.summary.
func (s *Server) replayBreadcrumbsState(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.replay.State(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// replayBreadcrumbsDelete acknowledges deletion of replay breadcrumb data.
// Consumer: replays/lib/seer_api.py — checks only the HTTP status code.
func (s *Server) replayBreadcrumbsDelete(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.replay.Delete(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}
