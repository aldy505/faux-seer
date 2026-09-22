// Package replay implements LLM-backed replay breadcrumb summarization.
// Sentry's replay ingestion polls the state endpoint until status reaches
// "completed" or "error", then reads data.summary for the AI summary.
package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
)

// Service summarizes replay breadcrumbs via an LLM and persists results.
type Service struct {
	store *db.Store
	llm   llm.Client
}

// New creates a replay service.
func New(store *db.Store, llmClient llm.Client) *Service {
	return &Service{store: store, llm: llmClient}
}

// StartRequest matches Sentry's ReplaySummaryStartRequest TypedDict
// (replays/lib/seer_api.py:17).
type StartRequest struct {
	ReplayID       string   `json:"replay_id"`
	ReplayStart    *string  `json:"replay_start"`
	ReplayEnd      *string  `json:"replay_end"`
	NumSegments    int64    `json:"num_segments"`
	OrganizationID int64    `json:"organization_id"`
	ProjectID      int64    `json:"project_id"`
	Temperature    *float64 `json:"temperature"`
}

// StateRequest matches Sentry's ReplaySummaryStateRequest TypedDict
// (replays/lib/seer_api.py:28).
type StateRequest struct {
	ReplayID       string `json:"replay_id"`
	OrganizationID int64  `json:"organization_id"`
	ProjectID      int64  `json:"project_id"`
}

// DeleteRequest matches Sentry's ReplayDeleteSeerDataRequest TypedDict
// (replays/lib/seer_api.py:34).
type DeleteRequest struct {
	ReplayIDs      []string `json:"replay_ids"`
	OrganizationID int64    `json:"organization_id"`
	ProjectID      int64    `json:"project_id"`
}

// Response matches the shape Sentry's React frontend renders:
// {"created_at": RFC3339, "status": str, "num_segments": *int,
//
//	"data": {"summary": str, "time_ranges": []}}.
//
// Consumer: replays/endpoints/project_replay_summary.py.
type Response struct {
	CreatedAt   string        `json:"created_at"`
	Status      string        `json:"status"`
	NumSegments *int64        `json:"num_segments,omitempty"`
	Data        *ResponseData `json:"data,omitempty"`
}

// ResponseData carries the AI summary content.
type ResponseData struct {
	Summary    string `json:"summary"`
	TimeRanges []any  `json:"time_ranges"`
}

// Start generates a breadcrumb summary for the given replay and persists it.
// The request carries only replay_id and num_segments, so the prompt states
// that raw segments are unavailable.
func (s *Service) Start(ctx context.Context, raw json.RawMessage) (Response, error) {
	var req StartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{}, fmt.Errorf("decode replay start request: %w", err)
	}
	if req.ReplayID == "" {
		return Response{}, fmt.Errorf("replay_id is required")
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Persist with processing status.
	if err := s.store.UpsertReplayBreadcrumbSummary(ctx, db.ReplayBreadcrumbSummaryRecord{
		ReplayID:       req.ReplayID,
		OrganizationID: &req.OrganizationID,
		ProjectID:      &req.ProjectID,
		Status:         "processing",
		CreatedAt:      now,
	}); err != nil {
		return Response{}, fmt.Errorf("upsert replay summary: %w", err)
	}

	// Generate summary via LLM. The request carries no raw segments, only
	// metadata, so the prompt must state that explicitly.
	// The request carries replay metadata only, so the summary must describe
	// what that metadata supports instead of inventing user activity.
	prompt := fmt.Sprintf(
		"Replay ID: %s\nNumber of segments: %d\n\n"+
			"The raw breadcrumb segments were not provided, so no user actions can be described.\n"+
			"State what the available metadata covers and that the breadcrumb details were unavailable.",
		req.ReplayID, req.NumSegments,
	)

	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: breadcrumbSystemPrompt,
		UserPrompt:   prompt,
		Temperature:  0.3,
		MaxTokens:    512,
	})

	summaryText := "Breadcrumbs summarized."
	if err == nil {
		var parsed struct {
			Summary string `json:"summary"`
		}
		if llm.ExtractJSON(text, &parsed) && parsed.Summary != "" {
			summaryText = parsed.Summary
		}
	}

	// Build the summary JSON payload.
	summaryData, _ := json.Marshal(ResponseData{
		Summary:    summaryText,
		TimeRanges: []any{},
	})

	completedAt := time.Now().UTC().Format(time.RFC3339)
	if err := s.store.UpsertReplayBreadcrumbSummary(ctx, db.ReplayBreadcrumbSummaryRecord{
		ReplayID:       req.ReplayID,
		OrganizationID: &req.OrganizationID,
		ProjectID:      &req.ProjectID,
		Status:         "completed",
		CreatedAt:      now,
		CompletedAt:    &completedAt,
		SummaryJSON:    summaryData,
	}); err != nil {
		return Response{}, fmt.Errorf("upsert replay summary completed: %w", err)
	}

	return Response{
		CreatedAt:   now,
		Status:      "completed",
		NumSegments: &req.NumSegments,
		Data: &ResponseData{
			Summary:    summaryText,
			TimeRanges: []any{},
		},
	}, nil
}

// State returns the stored replay breadcrumb summary, or a not_started
// placeholder when no summary exists for the given replay_id.
func (s *Service) State(ctx context.Context, raw json.RawMessage) (Response, error) {
	var req StateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return Response{}, fmt.Errorf("decode replay state request: %w", err)
	}
	if req.ReplayID == "" {
		return Response{}, fmt.Errorf("replay_id is required")
	}

	rec, err := s.store.GetReplayBreadcrumbSummary(ctx, req.ReplayID)
	if err != nil {
		return Response{}, fmt.Errorf("get replay summary: %w", err)
	}
	if rec == nil {
		return Response{
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Status:    "not_started",
			Data: &ResponseData{
				Summary:    "",
				TimeRanges: []any{},
			},
		}, nil
	}

	var data ResponseData
	if len(rec.SummaryJSON) > 0 {
		_ = json.Unmarshal(rec.SummaryJSON, &data)
	}
	if data.TimeRanges == nil {
		data.TimeRanges = []any{}
	}

	return Response{
		CreatedAt: rec.CreatedAt,
		Status:    rec.Status,
		Data:      &data,
	}, nil
}

// Delete removes stored replay breadcrumb summaries and returns success.
func (s *Service) Delete(ctx context.Context, raw json.RawMessage) (map[string]bool, error) {
	var req DeleteRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode replay delete request: %w", err)
	}
	if len(req.ReplayIDs) > 0 {
		if err := s.store.DeleteReplayBreadcrumbSummaries(ctx, req.ReplayIDs); err != nil {
			return nil, fmt.Errorf("delete replay summaries: %w", err)
		}
	}
	return map[string]bool{"success": true}, nil
}

// breadcrumbSystemPrompt keeps the reply schema out of the user message so a
// model echoing its instructions cannot return the schema's placeholders.
const breadcrumbSystemPrompt = "You summarize session replay breadcrumbs. Reply with exactly one JSON object: " +
	`{"summary": "<one or two sentences about what the breadcrumbs show>"}. No other text.`
