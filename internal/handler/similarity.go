package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Server) similarIssues(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.Similar(requestContext(r), body)
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

// supergroupClusterLightweight acknowledges Seer's lightweight RCA clustering
// request. faux-seer stores no clustering state, so it validates the payload and
// reports success without producing an artifact.
func (s *Server) supergroupClusterLightweight(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OrganizationID int64          `json:"organization_id"`
		GroupID        int64          `json:"group_id"`
		ProjectID      int64          `json:"project_id"`
		Issue          map[string]any `json:"issue"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode lightweight cluster request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) supergroupList(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.similarity.ListSupergroups(requestContext(r), body)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, response)
}
