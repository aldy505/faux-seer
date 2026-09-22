// Package autofix persists coding-agent state for Sentry autofix runs.
package autofix

import (
	"context"
	"encoding/json"
	"fmt"
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

// UpdateResponse is returned from the coding-agent state endpoints.
type UpdateResponse struct {
	RunID   int64   `json:"run_id"`
	Status  string  `json:"status,omitempty"`
	Message *string `json:"message,omitempty"`
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
