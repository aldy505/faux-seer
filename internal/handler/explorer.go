package handler

import "net/http"

func (s *Server) explorerChat(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.explorer.Chat(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) explorerState(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.explorer.GetState(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) explorerRuns(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.explorer.GetRuns(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) explorerUpdate(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.explorer.Update(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) explorerStatePR(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.explorer.GetStateByPR(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}
