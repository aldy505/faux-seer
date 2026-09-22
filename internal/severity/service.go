// Package severity implements compatibility severity scoring using an LLM
// with a deterministic token/heuristic fallback.
package severity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/aldy505/faux-seer/internal/llm"
)

// Service scores issue severity.
type Service struct{ llm llm.Client }

// New creates a severity service.
func New(llmClient llm.Client) *Service { return &Service{llm: llmClient} }

// Request matches Seer's SeverityScoreRequest TypedDict
// (event_manager.py:1975).
type Request struct {
	Message       string `json:"message"`
	HasStacktrace int    `json:"has_stacktrace"`
	Handled       *bool  `json:"handled,omitempty"`
	OrgID         *int64 `json:"org_id,omitempty"`
	ProjectID     *int64 `json:"project_id,omitempty"`
}

// Response matches Seer's severity response.
type Response struct {
	Severity float64 `json:"severity"`
}

// Score asks the LLM for a severity score and falls back to the
// deterministic heuristic if the model output is unparseable or the LLM
// call fails.
func (s *Service) Score(ctx context.Context, raw json.RawMessage) (Response, error) {
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return Response{}, fmt.Errorf("decode severity request: %w", err)
	}

	// Try the LLM path first.
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a severity scoring model. Reply with exactly one JSON object: {\"severity\": <float between 0.0 and 1.0>}. No other text. Higher severity means more critical.",
		UserPrompt: fmt.Sprintf(
			"Score the severity of this issue:\nMessage: %s\nHas stacktrace: %v\nHandled: %s",
			request.Message,
			request.HasStacktrace > 0,
			handledStr(request.Handled),
		),
		Temperature: 0.0,
		MaxTokens:   64,
	})
	if err == nil {
		var parsed struct {
			Severity *float64 `json:"severity"`
		}
		if llm.ExtractJSON(text, &parsed) && parsed.Severity != nil {
			score := math.Max(0, math.Min(1, *parsed.Severity))
			return Response{Severity: math.Round(score*100) / 100}, nil
		}
	}

	// Fallback: deterministic token/heuristic scoring.
	return heuristicScore(request), nil
}

// heuristicScore computes a severity score from message tokens and metadata.
// This is the original deterministic scoring used when the LLM is unavailable
// or returns unparseable output.
func heuristicScore(request Request) Response {
	score := 0.25
	message := strings.ToLower(request.Message)
	for _, token := range []string{"panic", "fatal", "segfault", "out of memory", "deadlock", "crash"} {
		if strings.Contains(message, token) {
			score += 0.18
		}
	}
	if request.HasStacktrace > 0 {
		score += 0.15
	}
	if request.Handled != nil && !*request.Handled {
		score += 0.15
	}
	if request.Handled != nil && *request.Handled {
		score -= 0.05
	}
	score = math.Max(0, math.Min(1, score))
	return Response{Severity: math.Round(score*100) / 100}
}

func handledStr(handled *bool) string {
	if handled == nil {
		return "unknown"
	}
	if *handled {
		return "true"
	}
	return "false"
}
