// Package explorer implements a compatibility-focused Seer Explorer backend.
package explorer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
)

// Service orchestrates minimal explorer chat/state compatibility.
type Service struct {
	store *db.Store
	llm   llm.Client
}

// New creates an explorer service.
func New(store *db.Store, llmClient llm.Client) *Service {
	return &Service{store: store, llm: llmClient}
}

// ChatResponse mirrors the explorer chat start/continue payload.
type ChatResponse struct {
	RunID                int64 `json:"run_id"`
	HasExplorerIndex     bool  `json:"has_explorer_index"`
	HasOrgProjectContext bool  `json:"has_org_project_context"`
}

// StateResponse mirrors the explorer state response.
type StateResponse struct {
	Session *RunState `json:"session"`
}

// RunsResponse mirrors the explorer runs response.
type RunsResponse struct {
	Data []AgentRun `json:"data"`
}

// UpdateResponse mirrors the explorer update response.
type UpdateResponse struct {
	RunID int64 `json:"run_id"`
}

// Message is a conversation message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

// MemoryBlock is a persisted explorer block.
type MemoryBlock struct {
	ID        string  `json:"id"`
	Message   Message `json:"message"`
	Timestamp string  `json:"timestamp"`
	Loading   bool    `json:"loading"`
}

// PendingUserInput is included for wire compatibility.
type PendingUserInput struct {
	ID        string         `json:"id"`
	InputType string         `json:"input_type"`
	Data      map[string]any `json:"data"`
}

// RepoPRState is included for wire compatibility.
type RepoPRState struct {
	RepoName         string  `json:"repo_name"`
	PRNumber         *int64  `json:"pr_number,omitempty"`
	PRURL            *string `json:"pr_url,omitempty"`
	PRID             *int64  `json:"pr_id,omitempty"`
	CommitSHA        *string `json:"commit_sha,omitempty"`
	PRCreationStatus *string `json:"pr_creation_status,omitempty"`
	PRCreationError  *string `json:"pr_creation_error,omitempty"`
	Title            *string `json:"title,omitempty"`
	Description      *string `json:"description,omitempty"`
}

// RunState is a SeerRunState-compatible subset.
type RunState struct {
	RunID            int64                  `json:"run_id"`
	Blocks           []MemoryBlock          `json:"blocks"`
	Status           string                 `json:"status"`
	UpdatedAt        string                 `json:"updated_at"`
	OwnerUserID      *int64                 `json:"owner_user_id,omitempty"`
	PendingUserInput *PendingUserInput      `json:"pending_user_input,omitempty"`
	RepoPRStates     map[string]RepoPRState `json:"repo_pr_states"`
}

// AgentRun is an AgentRun-compatible subset.
type AgentRun struct {
	RunID           int64   `json:"run_id"`
	Title           string  `json:"title"`
	LastTriggeredAt string  `json:"last_triggered_at"`
	CreatedAt       string  `json:"created_at"`
	UserID          *int64  `json:"user_id,omitempty"`
	CategoryKey     *string `json:"category_key,omitempty"`
	CategoryValue   *string `json:"category_value,omitempty"`
}

type chatRequest struct {
	OrganizationID int64          `json:"organization_id"`
	Query          string         `json:"query"`
	RunID          *int64         `json:"run_id"`
	InsertIndex    *int           `json:"insert_index"`
	OnPageContext  *string        `json:"on_page_context"`
	PageName       *string        `json:"page_name"`
	CategoryKey    *string        `json:"category_key"`
	CategoryValue  *string        `json:"category_value"`
	UserOrgContext map[string]any `json:"user_org_context"`
}

type stateRequest struct {
	OrganizationID int64 `json:"organization_id"`
	RunID          int64 `json:"run_id"`
}

type runsRequest struct {
	OrganizationID int64   `json:"organization_id"`
	UserID         *int64  `json:"user_id"`
	CategoryKey    *string `json:"category_key"`
	CategoryValue  *string `json:"category_value"`
	Offset         *int    `json:"offset"`
	Limit          *int    `json:"limit"`
}

type updateRequest struct {
	OrganizationID int64          `json:"organization_id"`
	RunID          int64          `json:"run_id"`
	Payload        map[string]any `json:"payload"`
}

type prStateRequest struct {
	OrganizationID int64  `json:"organization_id"`
	Provider       string `json:"provider"`
	PRID           int64  `json:"pr_id"`
}

// Chat starts or continues an explorer run.
func (s *Service) Chat(ctx context.Context, raw json.RawMessage) (ChatResponse, error) {
	var request chatRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return ChatResponse{}, fmt.Errorf("decode explorer chat request: %w", err)
	}
	request.Query = strings.TrimSpace(request.Query)
	if request.OrganizationID == 0 {
		return ChatResponse{}, fmt.Errorf("organization_id is required")
	}
	if request.Query == "" {
		return ChatResponse{}, fmt.Errorf("query is required")
	}
	if request.RunID == nil {
		return s.startRun(ctx, request)
	}
	return s.continueRun(ctx, request)
}

// GetState returns the current state for a run.
func (s *Service) GetState(ctx context.Context, raw json.RawMessage) (StateResponse, error) {
	var request stateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return StateResponse{}, fmt.Errorf("decode explorer state request: %w", err)
	}
	if request.OrganizationID == 0 {
		return StateResponse{}, fmt.Errorf("organization_id is required")
	}
	record, err := s.store.GetExplorerRun(ctx, request.RunID)
	if err != nil {
		return StateResponse{}, err
	}
	if record == nil || record.OrganizationID != request.OrganizationID {
		return StateResponse{Session: nil}, nil
	}
	state, err := decodeRunState(record.StateJSON)
	if err != nil {
		return StateResponse{}, err
	}
	return StateResponse{Session: &state}, nil
}

// GetRuns lists explorer runs.
func (s *Service) GetRuns(ctx context.Context, raw json.RawMessage) (RunsResponse, error) {
	var request runsRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return RunsResponse{}, fmt.Errorf("decode explorer runs request: %w", err)
	}
	if request.OrganizationID == 0 {
		return RunsResponse{}, fmt.Errorf("organization_id is required")
	}
	offset := 0
	if request.Offset != nil && *request.Offset > 0 {
		offset = *request.Offset
	}
	limit := 100
	if request.Limit != nil && *request.Limit > 0 {
		limit = *request.Limit
	}
	records, err := s.store.ListExplorerRuns(ctx, db.ExplorerRunFilter{
		OrganizationID: request.OrganizationID,
		UserID:         request.UserID,
		CategoryKey:    request.CategoryKey,
		CategoryValue:  request.CategoryValue,
		Offset:         offset,
		Limit:          limit,
	})
	if err != nil {
		return RunsResponse{}, err
	}
	runs := make([]AgentRun, 0, len(records))
	for _, record := range records {
		runs = append(runs, AgentRun{
			RunID:           record.ID,
			Title:           record.Title,
			LastTriggeredAt: record.LastTriggeredAt,
			CreatedAt:       record.CreatedAt,
			UserID:          record.UserID,
			CategoryKey:     record.CategoryKey,
			CategoryValue:   record.CategoryValue,
		})
	}
	return RunsResponse{Data: runs}, nil
}

// Update applies a compatibility-focused explorer update payload.
func (s *Service) Update(ctx context.Context, raw json.RawMessage) (UpdateResponse, error) {
	var request updateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return UpdateResponse{}, fmt.Errorf("decode explorer update request: %w", err)
	}
	if request.OrganizationID == 0 {
		return UpdateResponse{}, fmt.Errorf("organization_id is required")
	}
	record, err := s.store.GetExplorerRun(ctx, request.RunID)
	if err != nil {
		return UpdateResponse{}, err
	}
	if record == nil || record.OrganizationID != request.OrganizationID {
		return UpdateResponse{}, fmt.Errorf("explorer run not found")
	}
	state, err := decodeRunState(record.StateJSON)
	if err != nil {
		return UpdateResponse{}, err
	}
	payloadType, _ := request.Payload["type"].(string)
	switch payloadType {
	case "interrupt":
		state.Status = "completed"
	case "user_input_response":
		state.PendingUserInput = nil
	case "create_pr":
		repoName, _ := request.Payload["repo_name"].(string)
		if repoName != "" {
			completed := "completed"
			state.RepoPRStates[repoName] = RepoPRState{
				RepoName:         repoName,
				PRCreationStatus: &completed,
			}
		}
	}
	state.UpdatedAt = nowTimestamp()
	record.StateJSON, err = json.Marshal(state)
	if err != nil {
		return UpdateResponse{}, fmt.Errorf("marshal explorer update state: %w", err)
	}
	record.LastTriggeredAt = state.UpdatedAt
	if err := s.store.UpdateExplorerRun(ctx, *record); err != nil {
		return UpdateResponse{}, err
	}
	return UpdateResponse{RunID: request.RunID}, nil
}

// GetStateByPR returns a run state by provider/pr pair.
func (s *Service) GetStateByPR(ctx context.Context, raw json.RawMessage) (StateResponse, error) {
	var request prStateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return StateResponse{}, fmt.Errorf("decode explorer state/pr request: %w", err)
	}
	if request.OrganizationID == 0 {
		return StateResponse{}, fmt.Errorf("organization_id is required")
	}
	record, err := s.store.GetExplorerRunByPR(ctx, request.OrganizationID, request.Provider, request.PRID)
	if err != nil {
		return StateResponse{}, err
	}
	if record == nil {
		return StateResponse{Session: nil}, nil
	}
	state, err := decodeRunState(record.StateJSON)
	if err != nil {
		return StateResponse{}, err
	}
	return StateResponse{Session: &state}, nil
}

func (s *Service) startRun(ctx context.Context, request chatRequest) (ChatResponse, error) {
	userID := extractUserID(request.UserOrgContext)
	timestamp := nowTimestamp()
	userBlock := makeBlock("user", request.Query, 1, timestamp)
	assistantReply, err := s.generateReply(ctx, request.Query, request.PageName, request.OnPageContext)
	if err != nil {
		return ChatResponse{}, err
	}
	assistantBlock := makeBlock("assistant", assistantReply, 2, timestamp)
	state := RunState{
		Blocks:       []MemoryBlock{userBlock, assistantBlock},
		Status:       "completed",
		UpdatedAt:    timestamp,
		OwnerUserID:  userID,
		RepoPRStates: map[string]RepoPRState{},
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal explorer run state: %w", err)
	}
	record := db.ExplorerRunRecord{
		OrganizationID:  request.OrganizationID,
		UserID:          userID,
		Title:           summarizeTitle(request.Query),
		CategoryKey:     request.CategoryKey,
		CategoryValue:   request.CategoryValue,
		StateJSON:       stateJSON,
		CreatedAt:       timestamp,
		LastTriggeredAt: timestamp,
	}
	runID, err := s.store.CreateExplorerRun(ctx, record)
	if err != nil {
		return ChatResponse{}, err
	}
	state.RunID = runID
	stateJSON, err = json.Marshal(state)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal explorer run state with id: %w", err)
	}
	record.ID = runID
	record.StateJSON = stateJSON
	if err := s.store.UpdateExplorerRun(ctx, record); err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		RunID:                runID,
		HasExplorerIndex:     true,
		HasOrgProjectContext: true,
	}, nil
}

func (s *Service) continueRun(ctx context.Context, request chatRequest) (ChatResponse, error) {
	record, err := s.store.GetExplorerRun(ctx, *request.RunID)
	if err != nil {
		return ChatResponse{}, err
	}
	if record == nil || record.OrganizationID != request.OrganizationID {
		return ChatResponse{}, fmt.Errorf("explorer run not found")
	}
	state, err := decodeRunState(record.StateJSON)
	if err != nil {
		return ChatResponse{}, err
	}
	timestamp := nowTimestamp()
	userBlock := makeBlock("user", request.Query, len(state.Blocks)+1, timestamp)
	assistantReply, err := s.generateReply(ctx, request.Query, request.PageName, request.OnPageContext)
	if err != nil {
		return ChatResponse{}, err
	}
	assistantBlock := makeBlock("assistant", assistantReply, len(state.Blocks)+2, timestamp)
	blocks := state.Blocks
	if request.InsertIndex != nil {
		index := *request.InsertIndex
		if index < 0 {
			index = 0
		}
		if index < len(blocks) {
			blocks = append([]MemoryBlock{}, blocks[:index]...)
		}
	}
	blocks = append(blocks, userBlock, assistantBlock)
	state.Blocks = blocks
	state.Status = "completed"
	state.UpdatedAt = timestamp
	state.RunID = record.ID
	record.LastTriggeredAt = timestamp
	record.StateJSON, err = json.Marshal(state)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal continued explorer state: %w", err)
	}
	if err := s.store.UpdateExplorerRun(ctx, *record); err != nil {
		return ChatResponse{}, err
	}
	return ChatResponse{
		RunID:                record.ID,
		HasExplorerIndex:     true,
		HasOrgProjectContext: true,
	}, nil
}

func (s *Service) generateReply(ctx context.Context, query string, pageName, onPageContext *string) (string, error) {
	var prompt strings.Builder
	prompt.WriteString(strings.TrimSpace(query))
	if pageName != nil && strings.TrimSpace(*pageName) != "" {
		prompt.WriteString("\n\nPage:\n")
		prompt.WriteString(strings.TrimSpace(*pageName))
	}
	if onPageContext != nil && strings.TrimSpace(*onPageContext) != "" {
		prompt.WriteString("\n\nOn-page context:\n")
		prompt.WriteString(strings.TrimSpace(*onPageContext))
	}
	response, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are faux-seer, a compatibility-focused Sentry Explorer assistant. Answer directly using the supplied page context when it helps, and acknowledge uncertainty when context is incomplete.",
		UserPrompt:   prompt.String(),
		Temperature:  0.2,
		MaxTokens:    600,
	})
	if err != nil {
		return "", fmt.Errorf("generate explorer response: %w", err)
	}
	return strings.TrimSpace(response), nil
}

func decodeRunState(raw []byte) (RunState, error) {
	var state RunState
	if err := json.Unmarshal(raw, &state); err != nil {
		return RunState{}, fmt.Errorf("decode explorer run state: %w", err)
	}
	if state.RepoPRStates == nil {
		state.RepoPRStates = map[string]RepoPRState{}
	}
	return state, nil
}

func summarizeTitle(query string) string {
	title := strings.TrimSpace(strings.ReplaceAll(query, "\n", " "))
	if title == "" {
		return "Seer Explorer run"
	}
	const maxTitle = 120
	if len(title) <= maxTitle {
		return title
	}
	return strings.TrimSpace(title[:maxTitle-3]) + "..."
}

func extractUserID(context map[string]any) *int64 {
	if context == nil {
		return nil
	}
	value, ok := context["user_id"]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		id := int64(typed)
		return &id
	case int64:
		id := typed
		return &id
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return &parsed
		}
	case string:
		var parsed json.Number = json.Number(strings.TrimSpace(typed))
		if id, err := parsed.Int64(); err == nil {
			return &id
		}
	}
	return nil
}

func makeBlock(role, content string, sequence int, timestamp string) MemoryBlock {
	return MemoryBlock{
		ID:        fmt.Sprintf("block-%d", sequence),
		Message:   Message{Role: role, Content: content},
		Timestamp: timestamp,
		Loading:   false,
	}
}

func nowTimestamp() string { return nowString() }
