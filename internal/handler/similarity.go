package handler

import "net/http"

func (s *Server) similarIssues(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.Similar(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) groupingRecord(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.CreateGroupingRecords(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) deleteGroupingProject(w http.ResponseWriter, r *http.Request, _ []byte) {
	projectID := asInt64(r.PathValue("project_id"))
	response, err := s.similarity.DeleteProject(requestContext(r), projectID)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) deleteGroupingByHash(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.DeleteByHash(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) supergroupUpsert(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.UpsertSupergroup(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}

func (s *Server) supergroupList(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.ListSupergroups(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}
