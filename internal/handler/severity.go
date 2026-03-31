package handler

import "net/http"

func (s *Server) severityScore(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.severity.Score(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}
