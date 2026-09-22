package handler

import (
	"fmt"
	"net/http"
)

// assistedQueryStart starts a search-agent run and returns its id, which Sentry
// stores as the mirror's Seer run id before polling the state endpoint.
func (s *Server) assistedQueryStart(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.assistedQuery.Start(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// assistedQueryState returns the run state the trace-explorer frontend polls:
// the run status, an empty step list, and the final answer once the run has
// completed.
func (s *Server) assistedQueryState(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.assistedQuery.State(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// assistedQueryTranslate translates a natural-language question into
// trace-explorer queries. Sentry reads "responses" and "unsupported_reason", and
// indexes into query, stats_period, sort, group_by, visualization and mode on
// every entry.
func (s *Server) assistedQueryTranslate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.assistedQuery.Translate(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// assistedQueryTranslateAgentic is the agentic strategy variant of the
// translation endpoint; Sentry passes the response through to the frontend
// unchanged.
func (s *Server) assistedQueryTranslateAgentic(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.assistedQuery.TranslateAgentic(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// assistedQueryCreateCache acknowledges the trace-explorer setup request that
// primes cached prompts.
//
// SIMULATED: prompt caching is an internal Seer optimization; Sentry only checks
// the HTTP status and never reads the body.
func (s *Server) assistedQueryCreateCache(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode assisted-query create-cache request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
