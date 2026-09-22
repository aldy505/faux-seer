package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
)

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

func (s *Server) explorerRunsByIDs(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.explorer.GetRunsByIDs(requestContext(r), body)
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

// explorerRepos reports the repositories attached to a run. faux-seer does not
// track explorer repo selections, so it always reports an empty set.
func (s *Server) explorerRepos(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		RunID          int64 `json:"run_id"`
		OrganizationID int64 `json:"organization_id"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode explorer repos request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"repos": []any{}})
}

// explorerIndex acknowledges explorer indexing requests. faux-seer keeps no
// search index, so scheduling and exporting both report success immediately.
func (s *Server) explorerIndex(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// agentFeatureRun acknowledges an agent feature run. faux-seer runs no agent
// features, but Sentry's outbox requires a non-null run_id in the response.
func (s *Server) agentFeatureRun(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s.writeJSON(w, http.StatusOK, map[string]any{"success": true, "run_id": 1})
}
