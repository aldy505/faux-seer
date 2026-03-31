// Package autofix implements a compatibility-focused autofix workflow.
package autofix

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
)

// Service orchestrates autofix run state.
type Service struct {
	store *db.Store
	llm   llm.Client
}

// New creates an autofix service.
func New(store *db.Store, llmClient llm.Client) *Service {
	return &Service{store: store, llm: llmClient}
}

// StartRequest mirrors the high-level autofix request contract.
type StartRequest struct {
	OrganizationID int64           `json:"organization_id"`
	ProjectID      int64           `json:"project_id"`
	Issue          json.RawMessage `json:"issue"`
	Repos          []Repo          `json:"repos"`
	Additional     map[string]any  `json:"-"`
}

// Repo captures the repository fields Sentry sends.
type Repo struct {
	RepositoryID   *int64  `json:"repository_id,omitempty"`
	OrganizationID *int64  `json:"organization_id,omitempty"`
	IntegrationID  *string `json:"integration_id,omitempty"`
	Provider       string  `json:"provider"`
	Owner          string  `json:"owner"`
	Name           string  `json:"name"`
	ExternalID     string  `json:"external_id"`
	IsPrivate      *bool   `json:"is_private,omitempty"`
	BranchName     *string `json:"branch_name,omitempty"`
	Instructions   *string `json:"instructions,omitempty"`
	BaseCommitSHA  *string `json:"base_commit_sha,omitempty"`
	ProviderRaw    *string `json:"provider_raw,omitempty"`
}

// StartResponse is returned from the start endpoint.
type StartResponse struct {
	Started bool  `json:"started"`
	RunID   int64 `json:"run_id"`
}

// UpdateResponse is returned from the update endpoint.
type UpdateResponse struct {
	RunID   int64   `json:"run_id"`
	Status  string  `json:"status,omitempty"`
	Message *string `json:"message,omitempty"`
}

// StateResponse mirrors Seer's state endpoint response.
type StateResponse struct {
	GroupID *int64         `json:"group_id"`
	RunID   *int64         `json:"run_id"`
	State   map[string]any `json:"state"`
}

// PromptResponse returns a generated coding-agent prompt.
type PromptResponse struct {
	Prompt string `json:"prompt"`
}

// Start creates a new autofix run.
func (s *Service) Start(ctx context.Context, raw json.RawMessage) (StartResponse, error) {
	var request StartRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return StartResponse{}, fmt.Errorf("decode autofix start request: %w", err)
	}
	groupID := issueID(request.Issue)
	state := map[string]any{
		"run_id":            int64(0),
		"status":            "COMPLETED",
		"steps":             defaultSteps(request.Issue, request.Repos),
		"codebases":         codebases(request.Repos),
		"usage":             map[string]any{},
		"last_triggered_at": time.Now().UTC().Format(time.RFC3339Nano),
		"updated_at":        time.Now().UTC().Format(time.RFC3339Nano),
		"completed_at":      time.Now().UTC().Format(time.RFC3339Nano),
		"signals":           []string{},
		"feedback":          nil,
		"request":           rawMap(raw),
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return StartResponse{}, fmt.Errorf("marshal autofix state: %w", err)
	}
	runID, err := s.store.CreateAutofixRun(ctx, groupID, payload)
	if err != nil {
		return StartResponse{}, err
	}
	state["run_id"] = runID
	state["request"] = rawMap(raw)
	payload, err = json.Marshal(state)
	if err != nil {
		return StartResponse{}, fmt.Errorf("marshal autofix state with run id: %w", err)
	}
	if err := s.store.UpdateAutofixRun(ctx, runID, nil, nil, payload); err != nil {
		return StartResponse{}, err
	}
	return StartResponse{Started: true, RunID: runID}, nil
}

// Update mutates an autofix run based on a user payload.
func (s *Service) Update(ctx context.Context, raw json.RawMessage) (UpdateResponse, error) {
	var envelope struct {
		RunID   int64           `json:"run_id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return UpdateResponse{}, fmt.Errorf("decode autofix update request: %w", err)
	}
	record, err := s.store.GetAutofixRun(ctx, envelope.RunID)
	if err != nil {
		return UpdateResponse{}, err
	}
	if record == nil {
		message := "run not found"
		return UpdateResponse{RunID: envelope.RunID, Status: "error", Message: &message}, nil
	}
	state := rawMap(record.StateJSON)
	payloadMap := rawMap(envelope.Payload)
	payloadType, _ := payloadMap["type"].(string)
	appendProgress(state, payloadType, payloadMap)
	provider, prID := providerAndPR(payloadType, payloadMap)
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	if payloadType == "create_pr" || payloadType == "create_branch" {
		state["status"] = "COMPLETED"
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return UpdateResponse{}, fmt.Errorf("marshal updated autofix state: %w", err)
	}
	if err := s.store.UpdateAutofixRun(ctx, envelope.RunID, provider, prID, encoded); err != nil {
		return UpdateResponse{}, err
	}
	return UpdateResponse{RunID: envelope.RunID, Status: "success"}, nil
}

// GetState returns a run by run or group id.
func (s *Service) GetState(ctx context.Context, raw json.RawMessage) (StateResponse, error) {
	var request struct {
		GroupID *int64 `json:"group_id"`
		RunID   *int64 `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return StateResponse{}, fmt.Errorf("decode autofix state request: %w", err)
	}
	if request.RunID == nil {
		return StateResponse{GroupID: nil, RunID: nil, State: nil}, nil
	}
	record, err := s.store.GetAutofixRun(ctx, *request.RunID)
	if err != nil {
		return StateResponse{}, err
	}
	if record == nil {
		return StateResponse{GroupID: nil, RunID: nil, State: nil}, nil
	}
	return StateResponse{GroupID: record.GroupID, RunID: &record.ID, State: rawMap(record.StateJSON)}, nil
}

// GetStateByPR returns an autofix run associated with a provider/pr tuple.
func (s *Service) GetStateByPR(ctx context.Context, raw json.RawMessage) (StateResponse, error) {
	var request struct {
		Provider string `json:"provider"`
		PRID     int64  `json:"pr_id"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return StateResponse{}, fmt.Errorf("decode autofix state/pr request: %w", err)
	}
	record, err := s.store.GetAutofixRunByPR(ctx, request.Provider, request.PRID)
	if err != nil {
		return StateResponse{}, err
	}
	if record == nil {
		return StateResponse{GroupID: nil, RunID: nil, State: nil}, nil
	}
	return StateResponse{GroupID: record.GroupID, RunID: &record.ID, State: rawMap(record.StateJSON)}, nil
}

// GetPrompt returns a coding-agent prompt assembled from persisted run state.
func (s *Service) GetPrompt(ctx context.Context, raw json.RawMessage) (PromptResponse, error) {
	var request struct {
		RunID            int64 `json:"run_id"`
		IncludeRootCause bool  `json:"include_root_cause"`
		IncludeSolution  bool  `json:"include_solution"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return PromptResponse{}, fmt.Errorf("decode autofix prompt request: %w", err)
	}
	record, err := s.store.GetAutofixRun(ctx, request.RunID)
	if err != nil {
		return PromptResponse{}, err
	}
	if record == nil {
		return PromptResponse{Prompt: ""}, nil
	}
	state := rawMap(record.StateJSON)
	prompt := buildPrompt(state, request.IncludeRootCause, request.IncludeSolution)
	return PromptResponse{Prompt: prompt}, nil
}

// StoreCodingAgentStates stores coding-agent state snapshots.
func (s *Service) StoreCodingAgentStates(ctx context.Context, raw json.RawMessage) (UpdateResponse, error) {
	var request struct {
		RunID             int64            `json:"run_id"`
		CodingAgentStates []map[string]any `json:"coding_agent_states"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return UpdateResponse{}, fmt.Errorf("decode coding-agent state set request: %w", err)
	}
	record, err := s.store.GetAutofixRun(ctx, request.RunID)
	if err != nil {
		return UpdateResponse{}, err
	}
	if record == nil {
		message := "run not found"
		return UpdateResponse{RunID: request.RunID, Status: "error", Message: &message}, nil
	}
	state := rawMap(record.StateJSON)
	codingAgents := map[string]any{}
	for _, item := range request.CodingAgentStates {
		if id, _ := item["id"].(string); id != "" {
			codingAgents[id] = item
		}
	}
	state["coding_agents"] = codingAgents
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	payload, err := json.Marshal(state)
	if err != nil {
		return UpdateResponse{}, fmt.Errorf("marshal coding-agent state set payload: %w", err)
	}
	if err := s.store.UpdateAutofixRun(ctx, request.RunID, nil, nil, payload); err != nil {
		return UpdateResponse{}, err
	}
	return UpdateResponse{RunID: request.RunID, Status: "success"}, nil
}

// UpdateCodingAgentState updates a single coding-agent state snapshot.
func (s *Service) UpdateCodingAgentState(ctx context.Context, raw json.RawMessage) (UpdateResponse, error) {
	var request struct {
		AgentID string         `json:"agent_id"`
		Updates map[string]any `json:"updates"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return UpdateResponse{}, fmt.Errorf("decode coding-agent state update request: %w", err)
	}
	row := s.findRunByAgentID(ctx, request.AgentID)
	if row == nil {
		message := "agent not found"
		return UpdateResponse{RunID: 0, Status: "error", Message: &message}, nil
	}
	state := rawMap(row.StateJSON)
	codingAgents, _ := state["coding_agents"].(map[string]any)
	if codingAgents == nil {
		codingAgents = map[string]any{}
	}
	current, _ := codingAgents[request.AgentID].(map[string]any)
	if current == nil {
		current = map[string]any{"id": request.AgentID}
	}
	for key, value := range request.Updates {
		current[key] = value
	}
	codingAgents[request.AgentID] = current
	state["coding_agents"] = codingAgents
	payload, err := json.Marshal(state)
	if err != nil {
		return UpdateResponse{}, fmt.Errorf("marshal coding-agent state update payload: %w", err)
	}
	if err := s.store.UpdateAutofixRun(ctx, row.ID, nil, nil, payload); err != nil {
		return UpdateResponse{}, err
	}
	return UpdateResponse{RunID: row.ID, Status: "success"}, nil
}

func (s *Service) findRunByAgentID(ctx context.Context, agentID string) *db.AutofixRunRecord {
	for runID := int64(1); ; runID++ {
		record, err := s.store.GetAutofixRun(ctx, runID)
		if err != nil || record == nil {
			return nil
		}
		state := rawMap(record.StateJSON)
		codingAgents, _ := state["coding_agents"].(map[string]any)
		if _, ok := codingAgents[agentID]; ok {
			return record
		}
	}
}

func issueID(raw json.RawMessage) *int64 {
	issue := rawMap(raw)
	switch value := issue["id"].(type) {
	case float64:
		id := int64(value)
		return &id
	case int64:
		id := value
		return &id
	default:
		return nil
	}
}

func defaultSteps(issueRaw json.RawMessage, repos []Repo) []map[string]any {
	issue := rawMap(issueRaw)
	title := stringValue(issue["title"], "Issue investigation")
	rootCause := map[string]any{
		"id":               stepID("root-cause"),
		"key":              "root_cause_analysis",
		"title":            "Root cause analysis",
		"type":             "root_cause_analysis",
		"status":           "COMPLETED",
		"index":            0,
		"progress":         []any{},
		"causes":           []map[string]any{{"id": 0, "title": title, "description": "Compatibility mode generated a placeholder root-cause analysis from the issue payload."}},
		"selection":        map[string]any{"cause_id": 0},
		"completedMessage": "Root cause analysis generated.",
	}
	solution := map[string]any{
		"id":                stepID("solution"),
		"key":               "solution",
		"title":             "Proposed solution",
		"type":              "solution",
		"status":            "COMPLETED",
		"index":             1,
		"progress":          []any{},
		"solution":          []map[string]any{{"title": "Review the failing path", "description": "Inspect the stacktrace, affected repo, and deploy the minimal safe fix."}},
		"description":       "Generated compatibility-mode solution.",
		"solution_selected": true,
		"selected_mode":     "fix",
		"completedMessage":  "Solution ready.",
	}
	changes := map[string]any{
		"id":               stepID("changes"),
		"key":              "changes",
		"title":            "Suggested changes",
		"type":             "changes",
		"status":           "COMPLETED",
		"index":            2,
		"progress":         []any{},
		"changes":          suggestedChanges(repos),
		"completedMessage": "Suggested changes prepared.",
	}
	return []map[string]any{rootCause, solution, changes}
}

func suggestedChanges(repos []Repo) []map[string]any {
	if len(repos) == 0 {
		return []map[string]any{}
	}
	changes := make([]map[string]any, 0, len(repos))
	for _, repo := range repos {
		changes = append(changes, map[string]any{
			"repo_external_id": repo.ExternalID,
			"repo_name":        strings.TrimSpace(repo.Owner + "/" + repo.Name),
			"title":            "Investigate and apply a fix",
			"description":      "Compatibility mode does not edit code directly; use the generated prompt with your coding agent.",
			"diff":             []any{},
		})
	}
	return changes
}

func codebases(repos []Repo) map[string]any {
	result := make(map[string]any, len(repos))
	for _, repo := range repos {
		result[repo.ExternalID] = map[string]any{
			"repo_external_id": repo.ExternalID,
			"file_changes":     []any{},
			"is_readable":      true,
			"is_writeable":     true,
		}
	}
	return result
}

func appendProgress(state map[string]any, payloadType string, payload map[string]any) {
	steps, _ := state["steps"].([]any)
	if len(steps) == 0 {
		return
	}
	stepIndex := len(steps) - 1
	if explicit, ok := payload["step_index"].(float64); ok {
		if idx := int(explicit); idx >= 0 && idx < len(steps) {
			stepIndex = idx
		}
	}
	step, _ := steps[stepIndex].(map[string]any)
	progress, _ := step["progress"].([]any)
	progress = append(progress, map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"message":   fmt.Sprintf("Received autofix update: %s", payloadType),
		"type":      "INFO",
		"data":      payload,
	})
	step["progress"] = progress
	step["status"] = "COMPLETED"
	steps[stepIndex] = step
	state["steps"] = steps
	if payloadType == "feedback" {
		state["feedback"] = payload
	}
}

func providerAndPR(payloadType string, payload map[string]any) (*string, *int64) {
	if payloadType != "create_pr" && payloadType != "create_branch" {
		return nil, nil
	}
	provider := "github"
	if repoExternalID, ok := payload["repo_external_id"].(string); ok && repoExternalID != "" {
		provider = repoExternalID
	}
	prID := time.Now().UTC().Unix()
	return &provider, &prID
}

func buildPrompt(state map[string]any, includeRootCause, includeSolution bool) string {
	request, _ := state["request"].(map[string]any)
	issueTitle := issueTitle(request)
	repoNames := repoList(request)
	parts := []string{"Please fix the following issue. Ensure that your fix is fully working.", "Issue: " + issueTitle}
	if len(repoNames) > 0 {
		parts = append(parts, "Repositories: "+strings.Join(repoNames, ", "))
	}
	if includeRootCause {
		parts = append(parts, "Root cause: Compatibility mode generated a placeholder root-cause analysis from the issue payload.")
	}
	if includeSolution {
		parts = append(parts, "Solution: Inspect the failing path, implement the smallest safe fix, and add or update tests if needed.")
	}
	return strings.Join(parts, "\n\n")
}

func issueTitle(request map[string]any) string {
	issue, _ := request["issue"].(map[string]any)
	if issue == nil {
		return "Unknown issue"
	}
	for _, key := range []string{"title", "short_id", "id"} {
		if value := strings.TrimSpace(stringValue(issue[key], "")); value != "" {
			return value
		}
	}
	return "Unknown issue"
}

func repoList(request map[string]any) []string {
	repos, _ := request["repos"].([]any)
	result := make([]string, 0, len(repos))
	for _, repoAny := range repos {
		repo, _ := repoAny.(map[string]any)
		owner := stringValue(repo["owner"], "")
		name := stringValue(repo["name"], "")
		if owner == "" && name == "" {
			continue
		}
		result = append(result, strings.Trim(owner+"/"+name, "/"))
	}
	sort.Strings(result)
	return result
}

func rawMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func stringValue(value any, fallback string) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return fallback
	}
}

func stepID(prefix string) string { return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano()) }
