// Package runs persists and advances the generic background runs behind the
// investigations, assisted-query, and agent feature endpoints.
//
// Sentry addresses these runs by integer id and polls a projection of their
// workflow state, so the package owns one row per run plus a command log that
// makes repeated command submissions idempotent.
package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/runmgr"
)

// Kinds of run this package persists.
const (
	KindInvestigation = "investigation"
	KindAssistedQuery = "assisted_query"
	KindAgentFeature  = "agent_feature"
	KindCodeIndex     = "code_index"
)

// Task advances a run after it has been persisted.
type Task func(ctx context.Context, runID int64) error

// Service orchestrates persisted runs.
type Service struct {
	store *db.Store
	llm   llm.Client
	runs  *runmgr.Manager
}

// New creates a run service.
func New(store *db.Store, llmClient llm.Client, manager *runmgr.Manager) *Service {
	return &Service{store: store, llm: llmClient, runs: manager}
}

// AcceptResult reports how a command submission was recorded.
type AcceptResult struct {
	Version   int64
	Duplicate bool
	Record    *db.RunRecord
}

// Start persists a run and executes its task in the background.
func (s *Service) Start(ctx context.Context, record db.RunRecord, task Task) (*db.RunRecord, error) {
	id, err := s.store.CreateRun(ctx, record)
	if err != nil {
		return nil, err
	}
	record.ID = id
	s.dispatch(record.Kind, id, task)
	return &record, nil
}

// StartWithID persists a run under a caller-supplied id and executes its task.
//
// Sentry reports state for run ids it was given, so a request can arrive for a
// run faux-seer never created. The row is created on demand so the id Sentry
// already holds stays valid.
func (s *Service) StartWithID(ctx context.Context, id int64, record db.RunRecord, task Task) (*db.RunRecord, error) {
	if err := s.store.EnsureRun(ctx, id, record); err != nil {
		return nil, err
	}
	stored, err := s.store.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		stored = &record
		stored.ID = id
	}
	if task != nil {
		s.dispatch(stored.Kind, id, task)
	}
	return stored, nil
}

// FindByIdempotencyKey returns the run previously created for a provider
// idempotency key, so a retried request reuses its run instead of starting a
// second one.
func (s *Service) FindByIdempotencyKey(ctx context.Context, kind, key string) (*db.RunRecord, error) {
	return s.store.FindRunByIdempotencyKey(ctx, kind, key)
}

// Dispatch starts a task against an existing run.
func (s *Service) Dispatch(kind string, runID int64, task Task) {
	s.dispatch(kind, runID, task)
}

// Get returns a run, or nil when it does not exist.
func (s *Service) Get(ctx context.Context, runID int64) (*db.RunRecord, error) {
	return s.store.GetRun(ctx, runID)
}

// AcceptCommand records a command once per request id and advances the run's
// workflow version. A repeated request id is reported as a duplicate and leaves
// the stored command and version untouched.
func (s *Service) AcceptCommand(ctx context.Context, template db.RunRecord, requestID string, expectedVersion int64) (AcceptResult, error) {
	if err := s.store.EnsureRun(ctx, template.ID, template); err != nil {
		return AcceptResult{}, err
	}
	record, err := s.store.GetRun(ctx, template.ID)
	if err != nil {
		return AcceptResult{}, err
	}
	if record == nil {
		return AcceptResult{}, fmt.Errorf("run %d not found", template.ID)
	}
	if storedVersion, found, err := s.store.GetRunCommandVersion(ctx, template.ID, requestID); err != nil {
		return AcceptResult{}, err
	} else if found {
		return AcceptResult{Version: storedVersion, Duplicate: true, Record: record}, nil
	}

	version := record.WorkflowVersion
	if expectedVersion > version {
		version = expectedVersion
	}
	version++
	if _, err := s.store.AppendRunCommand(ctx, template.ID, requestID, version, nil); err != nil {
		return AcceptResult{}, err
	}
	record.WorkflowVersion = version
	if record.Status == db.RunStatusCompleted || record.Status == db.RunStatusError {
		record.Status = db.RunStatusProcessing
	}
	if err := s.store.UpdateRun(ctx, *record); err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{Version: version, Record: record}, nil
}

// Complete persists a successful result and marks the run completed.
func (s *Service) Complete(ctx context.Context, runID int64, result any) error {
	record, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("run %d not found", runID)
	}
	if result != nil {
		payload, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal run result: %w", err)
		}
		record.ResultJSON = payload
	}
	record.Status = db.RunStatusCompleted
	record.Error = nil
	return s.store.UpdateRun(ctx, *record)
}

// UpdateProjection stores a run's projection without changing its status.
func (s *Service) UpdateProjection(ctx context.Context, runID int64, projection map[string]any) error {
	record, err := s.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if record == nil {
		return fmt.Errorf("run %d not found", runID)
	}
	payload, err := json.Marshal(projection)
	if err != nil {
		return fmt.Errorf("marshal run projection: %w", err)
	}
	record.ResultJSON = payload
	return s.store.UpdateRun(ctx, *record)
}

// Fail marks a run failed. A cancelled context means the process is shutting
// down, which is persisted as the "shutdown" error.
func (s *Service) Fail(ctx context.Context, runID int64, cause error) error {
	message := cause.Error()
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		message = "shutdown"
	}
	writeCtx := context.WithoutCancel(ctx)
	record, err := s.store.GetRun(writeCtx, runID)
	if err != nil {
		return err
	}
	if record == nil {
		return cause
	}
	record.Status = db.RunStatusError
	record.Error = &message
	if err := s.store.UpdateRun(writeCtx, *record); err != nil {
		return err
	}
	return cause
}

// CompleteText runs a single LLM turn and returns the trimmed text answer.
func (s *Service) CompleteText(ctx context.Context, systemPrompt, prompt string, maxTokens int) (string, error) {
	if maxTokens <= 0 {
		maxTokens = 512
	}
	answer, err := s.llm.Complete(ctx, llm.CompletionRequest{SystemPrompt: systemPrompt, UserPrompt: prompt, Temperature: 0.1, MaxTokens: maxTokens})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

// Projection renders a run's stored result as the projection object Sentry
// reads. It never returns nil so the response serializes as {}.
func (s *Service) Projection(record *db.RunRecord) map[string]any {
	projection := map[string]any{}
	if record == nil || len(record.ResultJSON) == 0 {
		return projection
	}
	if err := json.Unmarshal(record.ResultJSON, &projection); err != nil {
		return map[string]any{}
	}
	return projection
}

// Status reports the run status, defaulting to processing for an unknown run.
func (s *Service) Status(record *db.RunRecord) string {
	if record == nil || record.Status == "" {
		return db.RunStatusProcessing
	}
	return record.Status
}

// RequestPayload decodes a run's stored request body.
func (s *Service) RequestPayload(record *db.RunRecord) map[string]any {
	payload := map[string]any{}
	if record == nil || len(record.RequestJSON) == 0 {
		return payload
	}
	if err := json.Unmarshal(record.RequestJSON, &payload); err != nil {
		return map[string]any{}
	}
	return payload
}

// dispatch starts a task, or runs it inline when no manager is configured.
func (s *Service) dispatch(kind string, runID int64, task Task) {
	if task == nil {
		return
	}
	if s.runs == nil {
		_ = task(context.Background(), runID)
		return
	}
	s.runs.Start(kind+":"+strconv.FormatInt(runID, 10), runmgr.TaskFunc(func(ctx context.Context) error {
		return task(ctx, runID)
	}))
}
