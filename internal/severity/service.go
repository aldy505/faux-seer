// Package severity implements compatibility severity scoring.
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

// Request matches Seer's legacy severity request.
type Request struct {
	Message       string `json:"message"`
	HasStacktrace int    `json:"has_stacktrace"`
	Handled       *bool  `json:"handled,omitempty"`
}

// Response matches Seer's severity response.
type Response struct {
	Severity float64 `json:"severity"`
}

// Score calculates a deterministic compatibility score.
func (s *Service) Score(_ context.Context, raw json.RawMessage) (Response, error) {
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return Response{}, fmt.Errorf("decode severity request: %w", err)
	}
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
	return Response{Severity: math.Round(score*100) / 100}, nil
}
