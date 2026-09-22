package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/preferences"
	"github.com/aldy505/faux-seer/internal/runmgr"
	"github.com/aldy505/faux-seer/internal/runs"
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

type repoResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Owner         string `json:"owner"`
	ExternalID    string `json:"external_id"`
	DefaultBranch string `json:"default_branch"`
	ReadAccess    bool   `json:"read_access"`
	WriteAccess   bool   `json:"write_access"`
}

func repoFromGit(repo git.Repo) repoResponse {
	return repoResponse{
		ID:            repo.ExternalID,
		Name:          repo.Name,
		Provider:      repo.Provider,
		Owner:         repo.Owner,
		ExternalID:    repo.ExternalID,
		DefaultBranch: repo.DefaultBranch,
		ReadAccess:    repo.HasReadAccess,
		WriteAccess:   repo.HasWriteAccess,
	}
}

// explorerRepos reports the repositories a run can work with.
//
// Sentry sends only the run and organization ids, so the repositories come from
// the organization's stored preferences when they exist, and otherwise from the
// configured repository provider. Without a provider there is nothing to report
// and the empty list is the simulated answer.
func (s *Server) explorerRepos(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		RunID          int64 `json:"run_id"`
		OrganizationID int64 `json:"organization_id"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode explorer repos request: %v", err))
		return
	}
	repos := s.configuredRepos(requestContext(r), request.OrganizationID)
	if s.git == nil {
		// SIMULATED: no repository provider is configured (set GITHUB_TOKEN), so
		// faux-seer can only report the repositories it already knows about.
		s.writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
		return
	}
	repositories, err := s.git.ListRepos(requestContext(r), "")
	if err != nil {
		s.log.ErrorContext(requestContext(r), "list explorer repositories", "error", err)
		s.writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
		return
	}
	for _, repo := range repositories {
		if len(repos) >= maxExplorerRepos {
			break
		}
		repos = append(repos, repoFromGit(repo))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

// maxExplorerRepos bounds the repository list returned to the frontend.
const maxExplorerRepos = 100

// configuredRepos renders the organization's stored repository preferences.
func (s *Server) configuredRepos(ctx context.Context, organizationID int64) []repoResponse {
	repos := []repoResponse{}
	if s.store == nil || organizationID == 0 {
		return repos
	}
	record, err := s.store.GetProjectPreference(ctx, organizationID)
	if err != nil || record == nil {
		return repos
	}
	var stored []preferences.Repository
	if err := json.Unmarshal(record.ReposJSON, &stored); err != nil {
		return repos
	}
	for _, repository := range stored {
		repos = append(repos, repoResponse{
			ID:          repository.ExternalID,
			Name:        repository.Name,
			Provider:    repository.Provider,
			Owner:       repository.Owner,
			ExternalID:  repository.ExternalID,
			ReadAccess:  true,
			WriteAccess: true,
		})
	}
	return repos
}

// removedRepoKey mirrors preferences.Service.RemovedSet's key format.
func removedRepoKey(provider, externalID string) string {
	return provider + "\x00" + externalID
}

// repoDetails is one repository entry in an org-repo-knowledge index request.
type repoDetails struct {
	ProjectIDs    []int64  `json:"project_ids"`
	Provider      string   `json:"provider"`
	Owner         string   `json:"owner"`
	Name          string   `json:"name"`
	ExternalID    string   `json:"external_id"`
	Languages     []string `json:"languages"`
	IntegrationID *string  `json:"integration_id"`
}

// explorerIndexOrgRepo indexes the repositories Sentry reports for an org.
func (s *Server) explorerIndexOrgRepo(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OrgID int64         `json:"org_id"`
		Repos []repoDetails `json:"repos"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode org repo knowledge index request: %v", err))
		return
	}
	if s.codeIndex == nil || !s.codeIndex.Indexed() {
		// SIMULATED: indexing needs repository credentials (set GITHUB_TOKEN).
		s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
		return
	}
	// Sentry removes repositories from Seer through the project-preference
	// endpoints; indexing honors those removals instead of re-adding the repo.
	removed, err := s.preferences.RemovedSet(requestContext(r), request.OrgID)
	if err != nil {
		s.log.ErrorContext(requestContext(r), "load removed repositories", "error", err, "org", request.OrgID)
		removed = map[string]struct{}{}
	}
	for _, repo := range request.Repos {
		if _, dropped := removed[removedRepoKey(repo.Provider, repo.ExternalID)]; dropped {
			continue
		}
		s.startIndexTask(codeindex.IndexRequest{
			OrganizationID: request.OrgID,
			Provider:       repo.Provider,
			Owner:          repo.Owner,
			Name:           repo.Name,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// explorerIndexProject acknowledges an org-project-knowledge index request.
//
// SIMULATED: Seer embeds the project metadata Sentry sends here (SDK, error and
// transaction counts, instrumentation, top transactions and span operations) so
// the explorer can answer project-level questions. faux-seer keeps no
// project-knowledge store, so the payload is validated and acknowledged.
func (s *Server) explorerIndexProject(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		OrgID    int64 `json:"org_id"`
		Projects []struct {
			ProjectID int64  `json:"project_id"`
			Slug      string `json:"slug"`
		} `json:"projects"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode org project knowledge index request: %v", err))
		return
	}
	// Project knowledge is stored in Sentry and sent to Seer as context, not
	// indexed here, so a successful ack is the whole contract.
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// explorerIndexGeneric acknowledges a project index request.
//
// SIMULATED: the request carries only org and project ids, so faux-seer has no
// repository identity to fetch. Sentry sends the repository details to
// /v1/automation/explorer/index/org-repo-knowledge, which does index them.
func (s *Server) explorerIndexGeneric(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		Projects []struct {
			OrgID     int64 `json:"org_id"`
			ProjectID int64 `json:"project_id"`
		} `json:"projects"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode explorer index request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// explorerIndexSentryKnowledge acknowledges indexing of Sentry's own
// documentation.
//
// SIMULATED: Sentry's documentation corpus is not reachable from faux-seer.
func (s *Server) explorerIndexSentryKnowledge(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode sentry knowledge index request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// explorerExportIndexes acknowledges an index export.
//
// SIMULATED: the export format and its destination are internal to Seer.
func (s *Server) explorerExportIndexes(w http.ResponseWriter, r *http.Request, body []byte) {
	if err := decodeOptionalJSONBody(body, new(any)); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode explorer export-indexes request: %v", err))
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// startIndexTask indexes one repository in the background.
func (s *Server) startIndexTask(request codeindex.IndexRequest) {
	if s.runs == nil {
		return
	}
	// Indexing reports progress through its terminal state only: failures are
	// logged by the run manager, and successes stay silent so the service keeps
	// its documented log contract (startup line, errors, and access records).
	s.runs.Start("code_index:"+request.Owner+"/"+request.Name, runmgr.TaskFunc(func(ctx context.Context) error {
		if _, err := s.codeIndex.IndexRepo(ctx, request); err != nil {
			return fmt.Errorf("index %s/%s: %w", request.Owner, request.Name, err)
		}
		return nil
	}))
}

// agentFeatureRun starts a feature run and returns the run id Sentry mirrors.
//
// The wire request carries an idempotency key, so a retried dispatch returns the
// run it already created instead of starting a second one.
func (s *Server) agentFeatureRun(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		FeatureID              string         `json:"feature_id"`
		Payload                map[string]any `json:"payload"`
		AgentRunOptions        map[string]any `json:"agent_run_options"`
		Referer                string         `json:"referrer"`
		Ref                    string         `json:"ref"`
		ExternalIdempotencyKey string         `json:"external_idempotency_key"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode agent feature run request: %v", err))
		return
	}
	ctx := requestContext(r)
	if request.ExternalIdempotencyKey != "" {
		existing, err := s.runStore.FindByIdempotencyKey(ctx, runs.KindAgentFeature, request.ExternalIdempotencyKey)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if existing != nil {
			s.writeJSON(w, http.StatusOK, map[string]any{"success": true, "run_id": existing.ID})
			return
		}
	}
	record := runRecord(runs.KindAgentFeature, body, request.ExternalIdempotencyKey)
	run, err := s.runStore.Start(ctx, record, s.featureTask(request.FeatureID, request.Payload))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"success": true, "run_id": run.ID})
}

// featureTask runs one feature dispatch: for a known feature it asks the model
// for a short result, and for an unknown feature it completes with no result
// rather than inventing one.
func (s *Server) featureTask(featureID string, payload map[string]any) runs.Task {
	return func(ctx context.Context, runID int64) error {
		rendered, err := json.Marshal(payload)
		if err != nil {
			rendered = []byte("{}")
		}
		prompt := fmt.Sprintf("Feature: %s\nPayload: %s", featureID, rendered)
		answer, err := s.runStore.CompleteText(ctx, "You run a single Sentry Seer agent feature and reply with the feature result in one short paragraph.", prompt, 500)
		if err != nil {
			return s.runStore.Fail(ctx, runID, err)
		}
		result := map[string]any{"feature_id": featureID}
		if strings.TrimSpace(answer) != "" {
			result["result"] = answer
		}
		return s.runStore.Complete(ctx, runID, result)
	}
}

// runRecord builds the persisted record for a run created from a request body.
func runRecord(kind string, body []byte, idempotencyKey string) db.RunRecord {
	record := db.RunRecord{Kind: kind, Status: db.RunStatusProcessing, RequestJSON: body}
	if idempotencyKey != "" {
		record.IdempotencyKey = &idempotencyKey
	}
	return record
}
