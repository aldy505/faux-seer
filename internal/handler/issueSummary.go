package handler

import "net/http"

func (s *Server) summarizeIssue(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.issueSummary.SummarizeIssue(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) summarizeTrace(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.issueSummary.SummarizeTrace(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) fixability(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.issueSummary.Fixability(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}
