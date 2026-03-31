// Package issuesummary implements issue and trace summarization.
package issuesummary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aldy505/faux-seer/internal/llm"
)

// Service creates compatibility summaries.
type Service struct{ llm llm.Client }

// New creates an issue summary service.
func New(llmClient llm.Client) *Service { return &Service{llm: llmClient} }

// SummarizeIssueResponse matches Seer's summarize-issue response.
type SummarizeIssueResponse struct {
	GroupID       string         `json:"group_id"`
	Headline      string         `json:"headline"`
	WhatsWrong    *string        `json:"whats_wrong,omitempty"`
	Trace         *string        `json:"trace,omitempty"`
	PossibleCause *string        `json:"possible_cause,omitempty"`
	Scores        map[string]any `json:"scores,omitempty"`
}

// SummarizeTraceResponse matches Seer's summarize-trace response.
type SummarizeTraceResponse struct {
	TraceID                    string           `json:"trace_id"`
	Summary                    string           `json:"summary"`
	KeyObservations            string           `json:"key_observations"`
	PerformanceCharacteristics string           `json:"performance_characteristics"`
	SuggestedInvestigations    []map[string]any `json:"suggested_investigations"`
}

// SummarizeIssue builds a compatibility issue summary.
func (s *Service) SummarizeIssue(ctx context.Context, raw json.RawMessage) (SummarizeIssueResponse, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return SummarizeIssueResponse{}, fmt.Errorf("decode issue summary request: %w", err)
	}
	groupID := fmt.Sprintf("%v", request["group_id"])
	issue, _ := request["issue"].(map[string]any)
	title := firstNonEmpty(stringField(issue, "title"), stringField(issue, "culprit"), "Issue summary")
	prompt := fmt.Sprintf("Summarize this issue for an engineer. Title: %s", title)
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{SystemPrompt: "You summarize issues in concise engineering language.", UserPrompt: prompt, Temperature: 0.1, MaxTokens: 256})
	if err != nil {
		return SummarizeIssueResponse{}, err
	}
	whatsWrong := firstSentence(text, title+" is causing user-visible failures.")
	trace := "Trace context was not provided."
	if request["trace_tree"] != nil {
		trace = "Trace context is available and should be reviewed alongside the issue."
	}
	cause := "Inspect the top in-app frames and recent deploys affecting the failing code path."
	return SummarizeIssueResponse{GroupID: groupID, Headline: title, WhatsWrong: &whatsWrong, Trace: &trace, PossibleCause: &cause, Scores: map[string]any{"possible_cause_confidence": 0.42, "possible_cause_novelty": 0.31, "fixability_score": 0.58, "fixability_score_version": 1, "is_fixable": true}}, nil
}

// Fixability returns a summarize-issue-compatible fixability response.
func (s *Service) Fixability(_ context.Context, raw json.RawMessage) (SummarizeIssueResponse, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return SummarizeIssueResponse{}, fmt.Errorf("decode fixability request: %w", err)
	}
	groupID := fmt.Sprintf("%v", request["group_id"])
	headline := "Fixability assessment"
	whatsWrong := "Compatibility mode returned a heuristic fixability score."
	trace := "Detailed trace analysis is not available in heuristic mode."
	cause := "This issue appears actionable if you can reproduce it locally or from stacktrace context."
	return SummarizeIssueResponse{GroupID: groupID, Headline: headline, WhatsWrong: &whatsWrong, Trace: &trace, PossibleCause: &cause, Scores: map[string]any{"fixability_score": 0.61, "fixability_score_version": 1, "is_fixable": true}}, nil
}

// SummarizeTrace builds a compatibility trace summary.
func (s *Service) SummarizeTrace(_ context.Context, raw json.RawMessage) (SummarizeTraceResponse, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return SummarizeTraceResponse{}, fmt.Errorf("decode trace summary request: %w", err)
	}
	traceID := fmt.Sprintf("%v", request["trace_id"])
	summary := firstNonEmpty(stringField(request, "summary"), "Trace contains a slow or failing execution path that should be inspected span-by-span.")
	return SummarizeTraceResponse{TraceID: traceID, Summary: summary, KeyObservations: "Review the longest-running spans, error spans, and service boundaries.", PerformanceCharacteristics: "Look for concentrated latency in the critical path and repeated downstream calls.", SuggestedInvestigations: []map[string]any{{"explanation": "Inspect the slowest transaction span and its child spans.", "span_id": "compat-span-1", "span_op": "http.server"}}}, nil
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if value, ok := m[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstSentence(text, fallback string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return fallback
	}
	if idx := strings.Index(trimmed, "."); idx > 0 {
		return strings.TrimSpace(trimmed[:idx+1])
	}
	return trimmed
}
