package handler

import "net/http"

func (s *Server) autofixStart(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.Start(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) autofixUpdate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.Update(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) autofixState(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.GetState(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) autofixStatePR(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.GetStateByPR(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) autofixPrompt(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.autofix.GetPrompt(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

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
