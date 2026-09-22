// Package autofix persists coding-agent state for Sentry autofix runs.
package autofix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/runmgr"
)

// Service orchestrates autofix run state.
type Service struct {
	store *db.Store
	llm   llm.Client
	runs  *runmgr.Manager
}

// New creates an autofix service. The run manager is optional; when nil the
// background agent-advancement task is skipped and the service operates in
// synchronous mode.
func New(store *db.Store, llmClient llm.Client, runs *runmgr.Manager) *Service {
	return &Service{store: store, llm: llmClient, runs: runs}
}

// UpdateResponse is returned from the coding-agent state endpoints.
type UpdateResponse struct {
	RunID   int64   `json:"run_id"`
	Status  string  `json:"status,omitempty"`
	Message *string `json:"message,omitempty"`
}

// StoreCodingAgentStates stores coding-agent state snapshots. When the run
// does not yet exist (Sentry reports state for runs Seer created), the service
// creates the run with the caller-supplied id and persists the state before
// returning. A background agent-advancement task is started when a run manager
// is configured.
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
		// Sentry reports coding-agent state for run ids Seer created, so
		// the id must be preserved. Create the run on demand with an
		// explicit id (idempotent for repeated state snapshots).
		state := map[string]any{}
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
		if err := s.store.CreateAutofixRunWithID(ctx, request.RunID, nil, payload); err != nil {
			return UpdateResponse{}, err
		}
		s.startAgentAdvancement(ctx, request.RunID, payload)
		return UpdateResponse{RunID: request.RunID, Status: "success"}, nil
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
	if err := s.store.UpdateAutofixRun(ctx, request.RunID, nil, nil, db.RunStatusProcessing, payload); err != nil {
		return UpdateResponse{}, err
	}
	s.startAgentAdvancement(ctx, request.RunID, payload)
	return UpdateResponse{RunID: request.RunID, Status: "success"}, nil
}

// UpdateCodingAgentState updates a single coding-agent state snapshot. The
// owning run is located by scanning all persisted runs for the agent id.
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
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	payload, err := json.Marshal(state)
	if err != nil {
		return UpdateResponse{}, fmt.Errorf("marshal coding-agent state update payload: %w", err)
	}
	if err := s.store.UpdateAutofixRun(ctx, row.ID, nil, nil, db.RunStatusProcessing, payload); err != nil {
		return UpdateResponse{}, err
	}
	return UpdateResponse{RunID: row.ID, Status: "success"}, nil
}

// findRunByAgentID scans all persisted runs and returns the one whose state
// blob contains the given agent id.
func (s *Service) findRunByAgentID(ctx context.Context, agentID string) *db.AutofixRunRecord {
	runs, err := s.store.ListAutofixRuns(ctx)
	if err != nil {
		return nil
	}
	for i := range runs {
		state := rawMap(runs[i].StateJSON)
		codingAgents, _ := state["coding_agents"].(map[string]any)
		if _, ok := codingAgents[agentID]; ok {
			return &runs[i]
		}
	}
	return nil
}

// startAgentAdvancement kicks off a background task that reads the stored
// coding-agent state, calls the LLM once to advance the agent, and persists
// the updated state. The task observes cancellation on shutdown and persists a
// terminal error status.
func (s *Service) startAgentAdvancement(ctx context.Context, runID int64, statePayload []byte) {
	if s.runs == nil {
		return
	}
	s.runs.Start(fmt.Sprintf("autofix:%d", runID), &agentAdvancementTask{
		store:  s.store,
		llm:    s.llm,
		runID:  runID,
		logger: slog.Default(),
		state:  statePayload,
	})
}

// agentAdvancementTask advances a coding agent by calling the LLM once.
type agentAdvancementTask struct {
	store  *db.Store
	llm    llm.Client
	runID  int64
	logger *slog.Logger
	state  []byte
}

// Run reads the stored state, calls the LLM to advance the coding agent, and
// persists the result. On cancellation the run is marked with an error status
// and the "shutdown" failure reason.
func (t *agentAdvancementTask) Run(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return t.fail("shutdown", ctx.Err())
	default:
	}

	state := rawMap(t.state)
	codingAgents, _ := state["coding_agents"].(map[string]any)
	if len(codingAgents) == 0 {
		return t.store.UpdateAutofixRun(ctx, t.runID, nil, nil, db.RunStatusCompleted, t.state)
	}

	agentSummary, err := json.Marshal(codingAgents)
	if err != nil {
		return t.fail(fmt.Sprintf("marshal coding agent state: %v", err), err)
	}
	prompt := fmt.Sprintf(
		"You are advancing a coding agent. Current agent state:\n%s\n\n"+
			"Return one JSON object keyed by agent id. For each existing agent id, give a short "+
			"description of its next action. Do not invent new agent ids.",
		agentSummary,
	)
	completion, err := t.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a coding agent assistant. Return valid JSON only.",
		UserPrompt:   prompt,
		MaxTokens:    1024,
	})
	if err != nil {
		reason := fmt.Sprintf("llm error: %v", err)
		if isCancellation(err) {
			// A cancelled provider call means the process is stopping, not
			// that the run failed on its own.
			reason = "shutdown"
		}
		return t.fail(reason, err)
	}

	// Merge the model's per-agent description into the agents Sentry reported.
	// Entries the model invented are ignored: the stored blob must keep
	// describing exactly the agents Sentry launched.
	var advanced map[string]map[string]any
	if err := json.Unmarshal([]byte(completion), &advanced); err == nil {
		for id, entry := range advanced {
			existing, ok := codingAgents[id].(map[string]any)
			if !ok {
				continue
			}
			if description, ok := entry["description"].(string); ok && strings.TrimSpace(description) != "" {
				existing["advancement"] = strings.TrimSpace(description)
			}
		}
	}
	state["coding_agents"] = codingAgents
	state["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal advanced agent state: %w", err)
	}
	return t.store.UpdateAutofixRun(ctx, t.runID, nil, nil, db.RunStatusCompleted, payload)
}

// fail persists a terminal error state. The write uses a live context because
// the run context may already be cancelled by the shutdown that caused it.
func (t *agentAdvancementTask) fail(reason string, cause error) error {
	t.logger.ErrorContext(context.Background(), "autofix agent advancement failed", "run", t.runID, "error", cause)
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := setFailureReason(t.state, reason)
	if err := t.store.UpdateAutofixRun(persistCtx, t.runID, nil, nil, db.RunStatusError, payload); err != nil {
		return fmt.Errorf("persist failed coding agent state: %w", err)
	}
	return cause
}

// isCancellation reports whether an error means the run was cancelled.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// setFailureReason sets the top-level failure_reason key on a run state blob.
// Sentry reads this field for run-level failure detail.
func setFailureReason(state []byte, reason string) []byte {
	m := rawMap(state)
	m["failure_reason"] = reason
	out, err := json.Marshal(m)
	if err != nil {
		return state
	}
	return out
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
