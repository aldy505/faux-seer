// Package explorer implements Seer's Explorer backend: chat runs execute in the
// background and are polled through the state endpoints.
package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/runmgr"
)

// Service orchestrates explorer chat runs.
type Service struct {
	store     *db.Store
	llm       llm.Client
	codeIndex *codeindex.Service
	runs      *runmgr.Manager
	// mu serializes state transitions. A run executes in the background while
	// update requests mutate the same record, and both write the whole state
	// blob, so read-modify-write sequences must not interleave.
	mu sync.Mutex
}

// New creates an explorer service. codeIndex and runs may be nil, in which case
// chat replies are generated without repository context and runs execute
// in-place.
func New(store *db.Store, llmClient llm.Client, codeIndex *codeindex.Service, runs *runmgr.Manager) *Service {
	return &Service{store: store, llm: llmClient, codeIndex: codeIndex, runs: runs}
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

// RunsResponse maps explorer run ids to their live run summaries.
type RunsResponse struct {
	Data map[string]AgentRun `json:"data"`
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
	// FailureReason is Sentry's own field for a classified failure; the run
	// status stays the primary signal, and this only adds detail.
	FailureReason *string `json:"failure_reason,omitempty"`
}

// AgentRun is an AgentRun-compatible subset.
type AgentRun struct {
	RunID           int64   `json:"run_id"`
	Status          string  `json:"status"`
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

type runsByIdsRequest struct {
	RunIDs []int64 `json:"run_ids"`
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

// GetRunsByIDs returns live run summaries for the requested run ids, keyed by
// run id. Unknown run ids are omitted.
func (s *Service) GetRunsByIDs(ctx context.Context, raw json.RawMessage) (RunsResponse, error) {
	var request runsByIdsRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return RunsResponse{}, fmt.Errorf("decode explorer runs by ids request: %w", err)
	}
	data := make(map[string]AgentRun, len(request.RunIDs))
	for _, runID := range request.RunIDs {
		record, err := s.store.GetExplorerRun(ctx, runID)
		if err != nil {
			return RunsResponse{}, err
		}
		if record == nil {
			continue
		}
		state, err := decodeRunState(record.StateJSON)
		if err != nil {
			return RunsResponse{}, err
		}
		data[strconv.FormatInt(record.ID, 10)] = AgentRun{
			RunID:           record.ID,
			Status:          state.Status,
			Title:           record.Title,
			LastTriggeredAt: record.LastTriggeredAt,
			CreatedAt:       record.CreatedAt,
			UserID:          record.UserID,
			CategoryKey:     record.CategoryKey,
			CategoryValue:   record.CategoryValue,
		}
	}
	return RunsResponse{Data: data}, nil
}

// Update applies an explorer update payload to the stored run.
//
// The payload types Sentry sends are interrupts, user-input responses, and PR
// creation requests. Each mutates the run under the service lock because a chat
// reply may be landing in the same record concurrently.
func (s *Service) Update(ctx context.Context, raw json.RawMessage) (UpdateResponse, error) {
	var request updateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return UpdateResponse{}, fmt.Errorf("decode explorer update request: %w", err)
	}
	if request.OrganizationID == 0 {
		return UpdateResponse{}, fmt.Errorf("organization_id is required")
	}
	payloadType, _ := request.Payload["type"].(string)

	s.mu.Lock()
	defer s.mu.Unlock()
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
	var resumeQuery string
	switch payloadType {
	case "interrupt":
		state.Blocks = dropLoadingBlocks(state.Blocks)
		state.Status = db.RunStatusCompleted
		state.PendingUserInput = nil
	case "user_input_response":
		resumeQuery = submittedInput(request.Payload)
		state.PendingUserInput = nil
		if resumeQuery == "" {
			state.Status = db.RunStatusCompleted
			break
		}
		state.Blocks = append(state.Blocks,
			makeBlock("user", resumeQuery, len(state.Blocks)+1, nowTimestamp()),
			makeLoadingBlock(len(state.Blocks)+2, nowTimestamp()),
		)
		state.Status = db.RunStatusProcessing
	case "awaiting_user_input":
		state.Status = db.RunStatusAwaiting
		state.PendingUserInput = pendingUserInput(request.Payload)
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
	state.FailureReason = nil
	state.UpdatedAt = nowTimestamp()
	state.RunID = record.ID
	record.Status = state.Status
	record.LastTriggeredAt = state.UpdatedAt
	record.StateJSON, err = json.Marshal(state)
	if err != nil {
		return UpdateResponse{}, fmt.Errorf("marshal explorer update state: %w", err)
	}
	if err := s.store.UpdateExplorerRun(ctx, *record); err != nil {
		return UpdateResponse{}, err
	}
	if resumeQuery != "" {
		s.dispatch(record.ID, request.OrganizationID, resumeQuery, nil, nil)
	}
	return UpdateResponse{RunID: request.RunID}, nil
}

// submittedInput extracts the user's answer from a user_input_response payload.
func submittedInput(payload map[string]any) string {
	for _, key := range []string{"response", "input", "content", "message", "text", "answer"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// pendingUserInput builds the persisted pending-input record from a payload.
func pendingUserInput(payload map[string]any) *PendingUserInput {
	raw, ok := payload["pending_user_input"].(map[string]any)
	if !ok {
		return &PendingUserInput{ID: "pending-input", InputType: "user_input", Data: map[string]any{}}
	}
	input := &PendingUserInput{ID: "pending-input", InputType: "user_input", Data: raw}
	if id, ok := raw["id"].(string); ok && id != "" {
		input.ID = id
	}
	if inputType, ok := raw["input_type"].(string); ok && inputType != "" {
		input.InputType = inputType
	}
	if data, ok := raw["data"].(map[string]any); ok {
		input.Data = data
	}
	return input
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
	state := RunState{
		Blocks: []MemoryBlock{
			makeBlock("user", request.Query, 1, timestamp),
			makeLoadingBlock(2, timestamp),
		},
		Status:       db.RunStatusProcessing,
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
		Status:          db.RunStatusProcessing,
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
	s.dispatch(runID, request.OrganizationID, request.Query, request.PageName, request.OnPageContext)
	return s.chatResponse(ctx, request.OrganizationID, runID), nil
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
	blocks = append(blocks,
		makeBlock("user", request.Query, len(blocks)+1, timestamp),
		makeLoadingBlock(len(blocks)+2, timestamp),
	)
	state.Blocks = blocks
	state.Status = db.RunStatusProcessing
	state.FailureReason = nil
	state.UpdatedAt = timestamp
	state.RunID = record.ID
	record.LastTriggeredAt = timestamp
	record.Status = state.Status
	record.StateJSON, err = json.Marshal(state)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("marshal continued explorer state: %w", err)
	}
	if err := s.store.UpdateExplorerRun(ctx, *record); err != nil {
		return ChatResponse{}, err
	}
	s.dispatch(record.ID, request.OrganizationID, request.Query, request.PageName, request.OnPageContext)
	return s.chatResponse(ctx, request.OrganizationID, record.ID), nil
}

// dispatch starts the background reply generation for a run.
func (s *Service) dispatch(runID, organizationID int64, query string, pageName, onPageContext *string) {
	if s.runs == nil {
		// Without a run manager the task still runs, but off the caller's
		// goroutine: dispatch can be reached while the caller holds the state
		// lock, which the task itself takes.
		task := &chatTask{service: s, runID: runID, organizationID: organizationID, query: query, pageName: pageName, onPageContext: onPageContext}
		go func() { _ = task.Run(context.Background()) }()
		return
	}
	s.runs.Start(fmt.Sprintf("explorer:%d", runID), &chatTask{
		service:        s,
		runID:          runID,
		organizationID: organizationID,
		query:          query,
		pageName:       pageName,
		onPageContext:  onPageContext,
	})
}

// chatResponse reports the run id plus which contexts the answer used.
func (s *Service) chatResponse(ctx context.Context, organizationID, runID int64) ChatResponse {
	hasIndex := false
	if s.codeIndex != nil {
		hasIndex, _ = s.codeIndex.HasOrgIndex(ctx, organizationID)
	}
	// faux-seer keeps a single index per organization, so repository knowledge
	// and project knowledge share one flag.
	return ChatResponse{RunID: runID, HasExplorerIndex: hasIndex, HasOrgProjectContext: hasIndex}
}

// chatTask generates the assistant reply for one chat turn.
type chatTask struct {
	service        *Service
	runID          int64
	organizationID int64
	query          string
	pageName       *string
	onPageContext  *string
}

// Run generates the reply and appends it to the run, or records the failure.
func (t *chatTask) Run(ctx context.Context) error {
	reply, err := t.service.generateReply(ctx, t.query, t.organizationID, t.pageName, t.onPageContext)
	if err != nil {
		return t.service.failRun(ctx, t.runID, err)
	}
	return t.service.completeRun(ctx, t.runID, reply)
}

// completeRun appends the assistant block and marks the run completed.
func (s *Service) completeRun(ctx context.Context, runID int64, reply string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.store.GetExplorerRun(ctx, runID)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("explorer run %d not found", runID)
	}
	state, err := decodeRunState(record.StateJSON)
	if err != nil {
		return err
	}
	timestamp := nowTimestamp()
	state.Blocks = resolveLoadingBlock(state.Blocks, reply, timestamp)
	state.Status = db.RunStatusCompleted
	state.FailureReason = nil
	state.UpdatedAt = timestamp
	state.RunID = record.ID
	record.Status = state.Status
	record.LastTriggeredAt = timestamp
	record.StateJSON, err = json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal completed explorer state: %w", err)
	}
	return s.store.UpdateExplorerRun(ctx, *record)
}

// failRun marks a run as failed with a classified reason (see classifyFailure)
// and drops the pending assistant block, so a terminal run never still claims to
// be loading. The state write uses a live context because the run context may
// already be cancelled.
func (s *Service) failRun(ctx context.Context, runID int64, cause error) error {
	message := classifyFailure(cause)
	writeCtx := context.WithoutCancel(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.store.GetExplorerRun(writeCtx, runID)
	if err != nil {
		return err
	}
	if record == nil {
		return cause
	}
	state, err := decodeRunState(record.StateJSON)
	if err != nil {
		return err
	}
	state.Blocks = dropLoadingBlocks(state.Blocks)
	state.Status = db.RunStatusError
	state.FailureReason = &message
	state.UpdatedAt = nowTimestamp()
	record.Status = state.Status
	record.LastTriggeredAt = state.UpdatedAt
	record.StateJSON, err = json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal failed explorer state: %w", err)
	}
	if err := s.store.UpdateExplorerRun(writeCtx, *record); err != nil {
		return err
	}
	return cause
}

// generateReply answers one explorer turn, grounding the answer in indexed
// repository code when the question looks code-related.
func (s *Service) generateReply(ctx context.Context, query string, organizationID int64, pageName, onPageContext *string) (string, error) {
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
	prompt.WriteString(s.codeContext(ctx, query, organizationID))
	response, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are faux-seer, a Sentry Explorer assistant. Answer directly using the supplied page context and repository code when it helps, and acknowledge uncertainty when context is incomplete.",
		UserPrompt:   prompt.String(),
		Temperature:  0.2,
		MaxTokens:    600,
	})
	if err != nil {
		return "", fmt.Errorf("generate explorer response: %w", err)
	}
	return strings.TrimSpace(response), nil
}

// codeExtensionPattern matches a source file reference such as "service.go".
var codeExtensionPattern = regexp.MustCompile(`\b[\w./-]+\.(go|py|ts|tsx|js|jsx|rb|java|kt|rs|c|h|cc|cpp|cs|php|swift|scala|sql|sh|yml|yaml)\b`)

// codeKeywords are the words that mark a question as being about source code.
var codeKeywords = []string{"function", "class", "method", "implement", "stacktrace", "stack trace", "traceback", "symbol", "repository", "repo ", "code", "file", "module", "package", "exception"}

// codeContext retrieves repository chunks for a code-related question. It never
// fails the reply: an unavailable or empty index simply adds no context.
func (s *Service) codeContext(ctx context.Context, query string, organizationID int64) string {
	if s.codeIndex == nil || organizationID == 0 || !looksLikeCodeQuery(query) {
		return ""
	}
	results, err := s.codeIndex.Search(ctx, query, codeindex.SearchOptions{OrganizationID: organizationID, K: 4})
	if err != nil || len(results) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n\nRelevant repository code:\n")
	for _, result := range results {
		builder.WriteString(fmt.Sprintf("\n%s:%d-%d (%s/%s@%s)\n", result.Path, result.StartLine, result.EndLine, result.Owner, result.Name, result.Ref))
		builder.WriteString(strings.TrimSpace(result.Text))
		builder.WriteString("\n")
	}
	return builder.String()
}

// looksLikeCodeQuery reports whether a question references source code.
func looksLikeCodeQuery(query string) bool {
	lowered := strings.ToLower(query)
	if codeExtensionPattern.MatchString(lowered) {
		return true
	}
	for _, keyword := range codeKeywords {
		if strings.Contains(lowered, keyword) {
			return true
		}
	}
	return false
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

// makeLoadingBlock builds the in-flight assistant block a run carries while its
// reply is being generated. Sentry renders a block with loading set as a pending
// message, so a slow provider shows progress instead of an empty session, and
// the block is filled in rather than duplicated when the reply lands.
func makeLoadingBlock(sequence int, timestamp string) MemoryBlock {
	block := makeBlock("assistant", "", sequence, timestamp)
	block.Loading = true
	return block
}

// resolveLoadingBlock fills in the pending assistant block, or appends the reply
// when the run has none, so a completed run always ends with exactly one
// assistant block for the turn.
func resolveLoadingBlock(blocks []MemoryBlock, reply, timestamp string) []MemoryBlock {
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Loading {
			blocks[i].Message.Content = reply
			blocks[i].Loading = false
			blocks[i].Timestamp = timestamp
			return blocks
		}
	}
	return append(blocks, makeBlock("assistant", reply, len(blocks)+1, timestamp))
}

// dropLoadingBlocks removes the pending assistant block when a run reaches a
// terminal state without an answer, so no terminal state claims to be loading.
func dropLoadingBlocks(blocks []MemoryBlock) []MemoryBlock {
	kept := make([]MemoryBlock, 0, len(blocks))
	for _, block := range blocks {
		if !block.Loading {
			kept = append(kept, block)
		}
	}
	return kept
}

// classifyFailure maps a failed run onto Sentry's own failure vocabulary:
// "shutdown" for a process shutdown, "timeout" for a provider that did not
// answer within OUTBOUND_TIMEOUT, and the underlying error text otherwise.
func classifyFailure(cause error) string {
	if errors.Is(cause, context.Canceled) {
		return "shutdown"
	}
	var timeout interface{ Timeout() bool }
	if errors.Is(cause, context.DeadlineExceeded) || (errors.As(cause, &timeout) && timeout.Timeout()) {
		return "timeout"
	}
	return cause.Error()
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
