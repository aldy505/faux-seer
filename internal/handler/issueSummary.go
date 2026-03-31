package handler

import (
	"context"
	"encoding/json"
	"net/http"
)

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

func (s *Server) getProjectPreference(w http.ResponseWriter, r *http.Request, body []byte) {
	request := readJSONMap(body)
	projectID := asInt64(request["project_id"])
	payload, err := s.autofixPreferenceGet(requestContext(r), projectID)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, map[string]any{"preference": payload})
}

func (s *Server) setProjectPreference(w http.ResponseWriter, r *http.Request, body []byte) {
	request := readJSONMap(body)
	pref, _ := request["preference"].(map[string]any)
	payload, err := s.autofixPreferenceSet(requestContext(r), pref)
	if err != nil {
		s.writeError(w, 400, err.Error())
		return
	}
	s.writeJSON(w, 200, map[string]any{"preference": payload})
}

func (s *Server) bulkGetProjectPreferences(w http.ResponseWriter, r *http.Request, body []byte) {
	request := readJSONMap(body)
	projectIDsAny, _ := request["project_ids"].([]any)
	preferences := make([]map[string]any, 0, len(projectIDsAny))
	for _, value := range projectIDsAny {
		projectID := asInt64(value)
		payload, err := s.autofixPreferenceGet(requestContext(r), projectID)
		if err != nil {
			s.writeError(w, 400, err.Error())
			return
		}
		if payload != nil {
			preferences = append(preferences, payload)
		}
	}
	s.writeJSON(w, 200, map[string]any{"preferences": preferences})
}

func (s *Server) bulkSetProjectPreferences(w http.ResponseWriter, r *http.Request, body []byte) {
	request := readJSONMap(body)
	items, _ := request["preferences"].([]any)
	preferences := make([]map[string]any, 0, len(items))
	for _, item := range items {
		pref, _ := item.(map[string]any)
		payload, err := s.autofixPreferenceSet(requestContext(r), pref)
		if err != nil {
			s.writeError(w, 400, err.Error())
			return
		}
		preferences = append(preferences, payload)
	}
	s.writeJSON(w, 200, map[string]any{"preferences": preferences})
}

func (s *Server) removeRepository(w http.ResponseWriter, r *http.Request, body []byte) {
	request := readJSONMap(body)
	organizationID := asInt64(request["organization_id"])
	repoProvider, _ := request["repo_provider"].(string)
	repoExternalID, _ := request["repo_external_id"].(string)
	_ = organizationID
	_ = repoProvider
	_ = repoExternalID
	s.writeJSON(w, 200, map[string]bool{"success": true})
}

func (s *Server) autofixPreferenceGet(ctx context.Context, projectID int64) (map[string]any, error) {
	payload, err := s.autofixStoreGet(ctx, projectID)
	if err != nil || payload == nil {
		return payload, err
	}
	return payload, nil
}

func (s *Server) autofixPreferenceSet(ctx context.Context, pref map[string]any) (map[string]any, error) {
	if pref == nil {
		pref = map[string]any{}
	}
	projectID := asInt64(pref["project_id"])
	organizationID := asInt64(pref["organization_id"])
	if _, ok := pref["repositories"]; !ok {
		pref["repositories"] = []any{}
	}
	if err := s.autofixStorePut(ctx, projectID, organizationID, pref); err != nil {
		return nil, err
	}
	return pref, nil
}

func cloneMap(input map[string]any) map[string]any {
	payload, _ := json.Marshal(input)
	var out map[string]any
	_ = json.Unmarshal(payload, &out)
	return out
}
