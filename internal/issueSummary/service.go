// Package issuesummary implements issue and trace summarization backed by
// an LLM with heuristic fallbacks so responses never contain empty
// required strings.
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
// Consumer: autofix/issue_summary.py — reads group_id, headline, whats_wrong,
// trace, possible_cause, scores.
type SummarizeIssueResponse struct {
	GroupID       string         `json:"group_id"`
	Headline      string         `json:"headline"`
	WhatsWrong    *string        `json:"whats_wrong,omitempty"`
	Trace         *string        `json:"trace,omitempty"`
	PossibleCause *string        `json:"possible_cause,omitempty"`
	Scores        map[string]any `json:"scores,omitempty"`
}

// SummarizeTraceResponse matches Seer's SummarizeTraceRequest result
// (seer/signed_seer_api.py:458).
type SummarizeTraceResponse struct {
	TraceID                    string           `json:"trace_id"`
	Summary                    string           `json:"summary"`
	KeyObservations            string           `json:"key_observations"`
	PerformanceCharacteristics string           `json:"performance_characteristics"`
	SuggestedInvestigations    []map[string]any `json:"suggested_investigations"`
}

// SummarizeIssueRequest matches Sentry's SummarizeIssueRequest TypedDict
// (seer/signed_seer_api.py:464).
type SummarizeIssueRequest struct {
	GroupID           int            `json:"group_id"`
	Issue             map[string]any `json:"issue"`
	TraceTree         any            `json:"trace_tree,omitempty"`
	OrganizationSlug  string         `json:"organization_slug"`
	OrganizationID    int            `json:"organization_id"`
	ProjectID         int            `json:"project_id"`
	ExperimentVariant *string        `json:"experiment_variant,omitempty"`
}

// SummarizeTraceRequest matches Sentry's SummarizeTraceRequest TypedDict
// (seer/signed_seer_api.py:458).
type SummarizeTraceRequest struct {
	TraceID         string         `json:"trace_id"`
	OnlyTransaction bool           `json:"only_transaction"`
	Trace           map[string]any `json:"trace"`
}

// SummarizeIssue builds a compatibility issue summary using the LLM.
func (s *Service) SummarizeIssue(ctx context.Context, raw json.RawMessage) (SummarizeIssueResponse, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return SummarizeIssueResponse{}, fmt.Errorf("decode issue summary request: %w", err)
	}
	groupID := fmt.Sprintf("%v", request["group_id"])
	issue, _ := request["issue"].(map[string]any)
	title := firstNonEmpty(stringField(issue, "title"), stringField(issue, "culprit"), "Issue summary")

	prompt := fmt.Sprintf(
		"Summarize this issue for an engineer.\nTitle: %s\nGroup ID: %s",
		title, groupID,
	)

	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: issueSummarySystemPrompt,
		UserPrompt:   prompt,
		Temperature:  0.1,
		MaxTokens:    256,
	})
	if err != nil {
		return SummarizeIssueResponse{}, err
	}

	var parsed struct {
		Headline      string `json:"headline"`
		WhatsWrong    string `json:"whats_wrong"`
		Trace         string `json:"trace"`
		PossibleCause string `json:"possible_cause"`
	}
	if llm.ExtractJSON(text, &parsed) {
		whatsWrong := firstNonEmpty(parsed.WhatsWrong, title+" is causing user-visible failures.")
		trace := firstNonEmpty(parsed.Trace, "Trace context was not provided.")
		if request["trace_tree"] != nil && trace == "Trace context was not provided." {
			trace = "Trace context is available and should be reviewed alongside the issue."
		}
		cause := firstNonEmpty(parsed.PossibleCause, "Inspect the top in-app frames and recent deploys affecting the failing code path.")
		headline := firstNonEmpty(parsed.Headline, title)

		return SummarizeIssueResponse{
			GroupID:       groupID,
			Headline:      headline,
			WhatsWrong:    &whatsWrong,
			Trace:         &trace,
			PossibleCause: &cause,
			Scores: map[string]any{
				"possible_cause_confidence": 0.42,
				"possible_cause_novelty":    0.31,
				"fixability_score":          0.58,
				"fixability_score_version":  1,
				"is_fixable":                true,
			},
		}, nil
	}

	// Fallback: deterministic summary.
	whatsWrong := title + " is causing user-visible failures."
	trace := "Trace context was not provided."
	if request["trace_tree"] != nil {
		trace = "Trace context is available and should be reviewed alongside the issue."
	}
	cause := "Inspect the top in-app frames and recent deploys affecting the failing code path."
	return SummarizeIssueResponse{
		GroupID:       groupID,
		Headline:      title,
		WhatsWrong:    &whatsWrong,
		Trace:         &trace,
		PossibleCause: &cause,
		Scores: map[string]any{
			"possible_cause_confidence": 0.42,
			"possible_cause_novelty":    0.31,
			"fixability_score":          0.58,
			"fixability_score_version":  1,
			"is_fixable":                true,
		},
	}, nil
}

// Fixability returns a fixability assessment using the LLM.
func (s *Service) Fixability(ctx context.Context, raw json.RawMessage) (SummarizeIssueResponse, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return SummarizeIssueResponse{}, fmt.Errorf("decode fixability request: %w", err)
	}
	groupID := fmt.Sprintf("%v", request["group_id"])

	// Try LLM path.
	prompt := fmt.Sprintf(
		"Assess the fixability of this issue.\nGroup ID: %s",
		groupID,
	)
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: fixabilitySystemPrompt,
		UserPrompt:   prompt,
		Temperature:  0.1,
		MaxTokens:    256,
	})
	if err == nil {
		var parsed struct {
			FixabilityScore float64 `json:"fixability_score"`
			IsFixable       *bool   `json:"is_fixable"`
			Headline        string  `json:"headline"`
			WhatsWrong      string  `json:"whats_wrong"`
			Trace           string  `json:"trace"`
			PossibleCause   string  `json:"possible_cause"`
		}
		if llm.ExtractJSON(text, &parsed) {
			isFixable := true
			if parsed.IsFixable != nil {
				isFixable = *parsed.IsFixable
			}
			score := parsed.FixabilityScore
			if score < 0 || score > 1 {
				score = 0.61
			}
			headline := firstNonEmpty(parsed.Headline, "Fixability assessment")
			whatsWrong := firstNonEmpty(parsed.WhatsWrong, "LLM returned a fixability assessment.")
			trace := firstNonEmpty(parsed.Trace, "Trace analysis context.")
			cause := firstNonEmpty(parsed.PossibleCause, "This issue appears actionable based on the available context.")

			return SummarizeIssueResponse{
				GroupID:       groupID,
				Headline:      headline,
				WhatsWrong:    &whatsWrong,
				Trace:         &trace,
				PossibleCause: &cause,
				Scores: map[string]any{
					"fixability_score":         score,
					"fixability_score_version": 1,
					"is_fixable":               isFixable,
				},
			}, nil
		}
	}

	// Fallback: deterministic fixability.
	headline := "Fixability assessment"
	whatsWrong := "Compatibility mode returned a heuristic fixability score."
	trace := "Detailed trace analysis is not available in heuristic mode."
	cause := "This issue appears actionable if you can reproduce it locally or from stacktrace context."
	return SummarizeIssueResponse{
		GroupID:       groupID,
		Headline:      headline,
		WhatsWrong:    &whatsWrong,
		Trace:         &trace,
		PossibleCause: &cause,
		Scores: map[string]any{
			"fixability_score":         0.61,
			"fixability_score_version": 1,
			"is_fixable":               true,
		},
	}, nil
}

// SummarizeTrace builds a trace summary using the LLM.
func (s *Service) SummarizeTrace(ctx context.Context, raw json.RawMessage) (SummarizeTraceResponse, error) {
	var request SummarizeTraceRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		// Try as a generic map for backwards compatibility.
		var generic map[string]any
		if err2 := json.Unmarshal(raw, &generic); err2 != nil {
			return SummarizeTraceResponse{}, fmt.Errorf("decode trace summary request: %w", err)
		}
		traceID := fmt.Sprintf("%v", generic["trace_id"])
		return s.summarizeTraceGeneric(ctx, traceID, generic)
	}
	return s.summarizeTrace(ctx, &request)
}

func (s *Service) summarizeTrace(ctx context.Context, req *SummarizeTraceRequest) (SummarizeTraceResponse, error) {
	// The trace payload is the request's evidence: it belongs in the user
	// message, while the reply schema lives in the system prompt.
	traceJSON, err := json.Marshal(req.Trace)
	if err != nil {
		return SummarizeTraceResponse{}, fmt.Errorf("marshal trace payload: %w", err)
	}
	prompt := fmt.Sprintf(
		"Summarize this trace for an engineer.\nTrace ID: %s\nOnly transaction: %v\nTrace: %s",
		req.TraceID, req.OnlyTransaction, traceJSON,
	)

	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: traceSummarySystemPrompt,
		UserPrompt:   prompt,
		Temperature:  0.1,
		MaxTokens:    512,
	})
	if err == nil {
		var parsed struct {
			Summary                    string           `json:"summary"`
			KeyObservations            string           `json:"key_observations"`
			PerformanceCharacteristics string           `json:"performance_characteristics"`
			SuggestedInvestigations    []map[string]any `json:"suggested_investigations"`
		}
		if llm.ExtractJSON(text, &parsed) {
			summary := firstNonEmpty(parsed.Summary, "Trace contains a slow or failing execution path that should be inspected span-by-span.")
			keyObs := firstNonEmpty(parsed.KeyObservations, "Review the longest-running spans, error spans, and service boundaries.")
			perf := firstNonEmpty(parsed.PerformanceCharacteristics, "Look for concentrated latency in the critical path and repeated downstream calls.")
			investigations := parsed.SuggestedInvestigations
			if len(investigations) == 0 {
				investigations = defaultInvestigations()
			}
			return SummarizeTraceResponse{
				TraceID:                    req.TraceID,
				Summary:                    summary,
				KeyObservations:            keyObs,
				PerformanceCharacteristics: perf,
				SuggestedInvestigations:    investigations,
			}, nil
		}
	}

	// Fallback: deterministic summary.
	return fallbackTraceSummary(req.TraceID), nil
}

func (s *Service) summarizeTraceGeneric(ctx context.Context, traceID string, request map[string]any) (SummarizeTraceResponse, error) {
	prompt := fmt.Sprintf(
		"Summarize this trace for an engineer.\nTrace ID: %s",
		traceID,
	)

	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: traceSummarySystemPrompt,
		UserPrompt:   prompt,
		Temperature:  0.1,
		MaxTokens:    512,
	})
	if err == nil {
		var parsed struct {
			Summary                    string           `json:"summary"`
			KeyObservations            string           `json:"key_observations"`
			PerformanceCharacteristics string           `json:"performance_characteristics"`
			SuggestedInvestigations    []map[string]any `json:"suggested_investigations"`
		}
		if llm.ExtractJSON(text, &parsed) {
			summary := firstNonEmpty(parsed.Summary, "Trace contains a slow or failing execution path that should be inspected span-by-span.")
			keyObs := firstNonEmpty(parsed.KeyObservations, "Review the longest-running spans, error spans, and service boundaries.")
			perf := firstNonEmpty(parsed.PerformanceCharacteristics, "Look for concentrated latency in the critical path and repeated downstream calls.")
			investigations := parsed.SuggestedInvestigations
			if len(investigations) == 0 {
				investigations = defaultInvestigations()
			}
			return SummarizeTraceResponse{
				TraceID:                    traceID,
				Summary:                    summary,
				KeyObservations:            keyObs,
				PerformanceCharacteristics: perf,
				SuggestedInvestigations:    investigations,
			}, nil
		}
	}

	return fallbackTraceSummary(traceID), nil
}

func fallbackTraceSummary(traceID string) SummarizeTraceResponse {
	return SummarizeTraceResponse{
		TraceID:                    traceID,
		Summary:                    "Trace contains a slow or failing execution path that should be inspected span-by-span.",
		KeyObservations:            "Review the longest-running spans, error spans, and service boundaries.",
		PerformanceCharacteristics: "Look for concentrated latency in the critical path and repeated downstream calls.",
		SuggestedInvestigations:    defaultInvestigations(),
	}
}

func defaultInvestigations() []map[string]any {
	return []map[string]any{{
		"explanation": "Inspect the slowest transaction span and its child spans.",
		"span_id":     "compat-span-1",
		"span_op":     "http.server",
	}}
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

// Prompt schemas live in the system message so a model echoing its instructions
// cannot leak example values into a summary, and the user message stays pure
// input data.
const (
	issueSummarySystemPrompt = "You summarize Sentry issues in concise engineering language. Reply with exactly one JSON object: " +
		`{"headline": "<one-line headline>", "whats_wrong": "<what is going wrong>", "trace": "<trace analysis>", "possible_cause": "<likely root cause>"}. No other text.`

	fixabilitySystemPrompt = "You assess how fixable a Sentry issue is. Reply with exactly one JSON object: " +
		`{"fixability_score": <0.0-1.0>, "is_fixable": <true or false>, "headline": "<one-line headline>", "whats_wrong": "<what is going wrong>", "trace": "<trace analysis>", "possible_cause": "<likely root cause>"}. No other text.`

	traceSummarySystemPrompt = "You analyze distributed traces. Reply with exactly one JSON object: " +
		`{"summary": "<overall summary>", "key_observations": "<key findings>", "performance_characteristics": "<performance analysis>", "suggested_investigations": [{"explanation": "<what to investigate>", "span_id": "<span id>", "span_op": "<span operation>"}]}. No other text.`
)
