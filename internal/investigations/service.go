// Package investigations implements Seer's investigation orchestration runs.
//
// Sentry creates an orchestration run, submits commands against it with an
// optimistic workflow version, and polls a projection of the run's state. The
// projection faux-seer produces is a subset of Seer's: the run summary plus the
// most recent command, because faux-seer does not implement Seer's workflow
// engine.
package investigations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/runs"
)

// Service orchestrates investigation runs.
type Service struct {
	runs *runs.Service
}

// New creates an investigations service.
func New(runService *runs.Service) *Service {
	return &Service{runs: runService}
}

// RunResponse matches Sentry's InvestigationRunResponse model.
type RunResponse struct {
	RunID      int64          `json:"runId"`
	Created    bool           `json:"created"`
	Projection map[string]any `json:"projection"`
}

// CommandResponse matches Sentry's InvestigationCommandResponse model.
type CommandResponse struct {
	RunID           int64          `json:"runId"`
	RequestID       string         `json:"requestId"`
	Accepted        bool           `json:"accepted"`
	Duplicate       bool           `json:"duplicate"`
	WorkflowVersion int64          `json:"workflowVersion"`
	Projection      map[string]any `json:"projection"`
}

// createRequest is the payload Sentry sends when it creates a run.
type createRequest struct {
	RequestID               string           `json:"requestId"`
	InvestigationID         int64            `json:"investigationId"`
	Source                  string           `json:"source"`
	ActiveTimeBudgetSeconds int64            `json:"activeTimeBudgetSeconds"`
	MonitoringProviders     []map[string]any `json:"monitoringProviders"`
}

// commandRequest is the payload Sentry sends when it submits a command.
type commandRequest struct {
	RequestID               string           `json:"requestId"`
	ExpectedWorkflowVersion int64            `json:"expectedWorkflowVersion"`
	Command                 map[string]any   `json:"command"`
	MonitoringProviders     []map[string]any `json:"monitoringProviders"`
}

// Create starts an investigation run, or returns the run already created for
// the same request id.
func (s *Service) Create(ctx context.Context, raw json.RawMessage) (RunResponse, error) {
	var request createRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return RunResponse{}, fmt.Errorf("decode investigation create request: %w", err)
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return RunResponse{}, fmt.Errorf("requestId is required")
	}
	existing, err := s.runs.FindByIdempotencyKey(ctx, runs.KindInvestigation, request.RequestID)
	if err != nil {
		return RunResponse{}, err
	}
	if existing != nil {
		// A retried create is not a new run: Sentry keys its mirror on the id.
		return RunResponse{RunID: existing.ID, Created: false, Projection: s.runs.Projection(existing)}, nil
	}
	requestID := request.RequestID
	record := db.RunRecord{
		Kind:           runs.KindInvestigation,
		Status:         db.RunStatusProcessing,
		RequestJSON:    raw,
		IdempotencyKey: &requestID,
	}
	run, err := s.runs.Start(ctx, record, s.advanceTask(startupPrompt(request)))
	if err != nil {
		return RunResponse{}, err
	}
	return RunResponse{RunID: run.ID, Created: true, Projection: map[string]any{}}, nil
}

// Command records a command against a run, creating the run if Sentry addresses
// one faux-seer never created. requestID is the UUID Sentry must see echoed
// back, so the caller normalizes it before the command is recorded.
func (s *Service) Command(ctx context.Context, runID int64, requestID string, raw json.RawMessage) (CommandResponse, error) {
	var request commandRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return CommandResponse{}, fmt.Errorf("decode investigation command request: %w", err)
	}
	if strings.TrimSpace(requestID) == "" {
		return CommandResponse{}, fmt.Errorf("requestId is required")
	}
	template := db.RunRecord{
		ID:              runID,
		Kind:            runs.KindInvestigation,
		Status:          db.RunStatusProcessing,
		RequestJSON:     nil,
		WorkflowVersion: 1,
	}
	result, err := s.runs.AcceptCommand(ctx, template, requestID, request.ExpectedWorkflowVersion)
	if err != nil {
		return CommandResponse{}, err
	}
	projection := s.runs.Projection(result.Record)
	if !result.Duplicate {
		projection = withCommand(projection, request.Command, result.Version)
		if err := s.runs.UpdateProjection(ctx, runID, projection); err != nil {
			return CommandResponse{}, err
		}
		s.runs.Dispatch(result.Record.Kind, runID, s.advanceTask(commandPrompt(request.Command)))
	}
	return CommandResponse{
		RunID:           runID,
		RequestID:       requestID,
		Accepted:        true,
		Duplicate:       result.Duplicate,
		WorkflowVersion: result.Version,
		Projection:      projection,
	}, nil
}

// Get returns a run's current projection.
func (s *Service) Get(ctx context.Context, runID int64) (RunResponse, error) {
	record, err := s.runs.Get(ctx, runID)
	if err != nil {
		return RunResponse{}, err
	}
	return RunResponse{RunID: runID, Created: record != nil, Projection: s.runs.Projection(record)}, nil
}

// advanceTask runs one LLM step and merges the answer into the run projection.
func (s *Service) advanceTask(prompt string) runs.Task {
	return func(ctx context.Context, runID int64) error {
		record, err := s.runs.Get(ctx, runID)
		if err != nil {
			return err
		}
		if record == nil {
			return fmt.Errorf("investigation run %d not found", runID)
		}
		projection := s.runs.Projection(record)
		answer, err := s.runs.CompleteText(ctx, investigationSystemPrompt, prompt, 700)
		if err != nil {
			return s.runs.Fail(ctx, runID, err)
		}
		projection["summary"] = answer
		projection["status"] = "awaiting_command"
		projection["workflow_version"] = record.WorkflowVersion
		return s.runs.UpdateProjection(context.WithoutCancel(ctx), runID, projection)
	}
}

const investigationSystemPrompt = "You are an incident investigation orchestrator. Given the investigation context, state the most useful next diagnostic step and what you expect to learn. Answer in at most four sentences."

// startupPrompt describes a new investigation run for the model.
func startupPrompt(request createRequest) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Investigation %d was opened (source: %s, active time budget: %ds).",
		request.InvestigationID, request.Source, request.ActiveTimeBudgetSeconds)
	if len(request.MonitoringProviders) > 0 {
		providers, err := json.Marshal(request.MonitoringProviders)
		if err == nil {
			fmt.Fprintf(&builder, " Monitoring providers: %s.", providers)
		}
	}
	builder.WriteString(" Propose the investigation plan.")
	return builder.String()
}

// commandPrompt describes a submitted command for the model.
func commandPrompt(command map[string]any) string {
	rendered, err := json.Marshal(command)
	if err != nil {
		return "A command was submitted. Describe the next diagnostic step."
	}
	return fmt.Sprintf("A command was submitted: %s. Describe the next diagnostic step and its expected outcome.", rendered)
}

// withCommand records the latest accepted command in the projection.
func withCommand(projection map[string]any, command map[string]any, version int64) map[string]any {
	if projection == nil {
		projection = map[string]any{}
	}
	projection["last_command"] = command
	projection["workflow_version"] = version
	projection["status"] = "processing"
	return projection
}
