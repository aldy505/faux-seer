package handler

import "net/http"

func (s *Server) codingAgentStateSet(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.StoreCodingAgentStates(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) codingAgentStateUpdate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.UpdateCodingAgentState(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}
