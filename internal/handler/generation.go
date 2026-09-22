package handler

import (
	"net/http"
)

// oneshotRun dispatches a synchronous one-shot LLM task. Consumer:
// seer/oneshot.py:121 — reads data["result"] from the response, then
// extracts oneshot-specific keys (e.g. "title" from conversation_title,
// "answer" from agent_question). Missing keys are handled gracefully.
func (s *Server) oneshotRun(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.generation.OneShot(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// unitTestsGenerate generates unit tests for a pull request. Consumer:
// seer/services/test_generation/impl.py:28 — start_unit_test_generation reads
// only the HTTP status and treats anything other than 200 as a failure, so the
// body is returned for humans and tooling rather than for Sentry.
func (s *Server) unitTestsGenerate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.generation.UnitTests(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}
