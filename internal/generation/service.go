// Package generation implements LLM-backed generation endpoints: generic
// LLM generation, one-shot tasks, and unit-test code generation.
package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/llm"
)

// Service generates text, one-shot results, and unit tests via LLM calls.
type Service struct {
	llm      llm.Client
	provider git.Provider
	cfg      *config.Config
}

// New creates a generation service.
func New(llmClient llm.Client, provider git.Provider, cfg *config.Config) *Service {
	return &Service{llm: llmClient, provider: provider, cfg: cfg}
}

// GenerateRequest matches Sentry's LlmGenerateRequest TypedDict
// (seer/signed_seer_api.py:259).
type GenerateRequest struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	Referrer       string  `json:"referrer"`
	Prompt         string  `json:"prompt"`
	SystemPrompt   string  `json:"system_prompt"`
	Temperature    float64 `json:"temperature"`
	MaxTokens      int     `json:"max_tokens"`
	ResponseSchema any     `json:"response_schema,omitempty"`
	Timeout        any     `json:"timeout,omitempty"`
	Reasoning      any     `json:"reasoning,omitempty"`
	ConversationID any     `json:"conversation_id,omitempty"`
}

// GenerateResponse is {"content": string, "model": string}.
// Consumer: external_issues.py:98, issue_view_title_generate.py:74,
// autofix_issue_data.py:200, seer_assertions.py:365.
type GenerateResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

// OneShotRequest matches Sentry's OneShotRunRequest TypedDict
// (seer/signed_seer_api.py:281).
type OneShotRequest struct {
	OneshotID string         `json:"oneshot_id"`
	Payload   map[string]any `json:"payload"`
}

// OneShotResponse is {"result": map[string]any}.
// Consumer: seer/oneshot.py:121 — data.get("result") or {}.
type OneShotResponse struct {
	Result map[string]any `json:"result"`
}

// UnitTestRequest matches Sentry's UnitTestGenerationRequest TypedDict
// (seer/signed_seer_api.py:276).
type UnitTestRequest struct {
	Repo map[string]any `json:"repo"`
	PRID int            `json:"pr_id"`
}

// UnitTestResponse is {"success": bool, "tests": string}.
// Consumer: seer/services/test_generation/impl.py:28 — response.status == 200.
type UnitTestResponse struct {
	Success bool   `json:"success"`
	Tests   string `json:"tests,omitempty"`
}

// Generate calls the LLM with the request's prompt and returns the result.
func (s *Service) Generate(ctx context.Context, raw json.RawMessage) (GenerateResponse, error) {
	var req GenerateRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return GenerateResponse{}, fmt.Errorf("decode LLM generate request: %w", err)
	}
	model := ""
	if len(s.cfg.LLMModel) > 0 {
		model = s.cfg.LLMModel[0]
	}
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: req.SystemPrompt,
		UserPrompt:   req.Prompt,
		Temperature:  req.Temperature,
		MaxTokens:    req.MaxTokens,
	})
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("LLM generate call: %w", err)
	}
	return GenerateResponse{Content: text, Model: model}, nil
}

// OneShot dispatches a synchronous one-shot LLM task. The result keys vary
// by oneshot_id: "conversation_title" returns {"title": ...},
// "agent_question" returns {"answer": ...}. Unknown ids default to
// {"answer": ...}.
func (s *Service) OneShot(ctx context.Context, raw json.RawMessage) (OneShotResponse, error) {
	var req OneShotRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return OneShotResponse{}, fmt.Errorf("decode oneshot request: %w", err)
	}
	// Build a prompt from the oneshot_id and payload.
	payloadBytes, _ := json.Marshal(req.Payload)
	prompt := fmt.Sprintf("One-shot task: %s\nPayload: %s", req.OneshotID, string(payloadBytes))

	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a one-shot task executor. Reply with the result as a JSON object.",
		UserPrompt:   prompt,
		Temperature:  0.2,
		MaxTokens:    512,
	})
	if err != nil {
		return OneShotResponse{}, fmt.Errorf("oneshot LLM call: %w", err)
	}

	// Parse the model output for a JSON object. A model that echoes its prompt
	// would hand back the request payload, which is never a valid one-shot
	// result, so an echoed payload falls through to the id-specific key.
	var parsed map[string]any
	if llm.ExtractJSON(text, &parsed) && !reflect.DeepEqual(parsed, req.Payload) {
		return OneShotResponse{Result: parsed}, nil
	}

	// Build a result with a key appropriate for the oneshot_id.
	result := make(map[string]any)
	switch req.OneshotID {
	case "conversation_title":
		// Consumer: ai_monitoring/conversation_titles.py:233 — result.get("title")
		result["title"] = strings.TrimSpace(text)
	case "agent_question":
		// Consumer: seer/run_questions.py — result.get("answer")
		result["answer"] = strings.TrimSpace(text)
	default:
		result["answer"] = strings.TrimSpace(text)
	}
	return OneShotResponse{Result: result}, nil
}

// UnitTests generates unit tests for a PR's changed file. When a git provider
// is configured it fetches the PR diff and asks the LLM to write tests;
// otherwise it returns a simulated success.
func (s *Service) UnitTests(ctx context.Context, raw json.RawMessage) (UnitTestResponse, error) {
	var req UnitTestRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return UnitTestResponse{}, fmt.Errorf("decode unit test request: %w", err)
	}

	// SIMULATED: no repository can be read without a configured git provider.
	if s.provider == nil {
		return UnitTestResponse{Success: true}, nil
	}

	// Extract repo details from the request.
	repo := req.Repo
	provider, _ := repo["provider"].(string)
	owner, _ := repo["owner"].(string)
	name, _ := repo["name"].(string)
	if provider == "" || owner == "" || name == "" {
		return UnitTestResponse{Success: true}, nil
	}

	// A missing pull request is a client mistake, not a transient failure, and
	// Sentry only reads the status code, so it still gets a success ack.
	pr, err := s.provider.GetPR(ctx, owner, name, req.PRID)
	if err != nil {
		if git.IsNotFound(err) {
			return UnitTestResponse{Success: true}, nil
		}
		return UnitTestResponse{}, fmt.Errorf("fetch pull request %s/%s#%d: %w", owner, name, req.PRID, err)
	}

	files, err := s.provider.GetPRFiles(ctx, owner, name, req.PRID)
	if err != nil {
		if git.IsNotFound(err) {
			return UnitTestResponse{Success: true}, nil
		}
		return UnitTestResponse{}, fmt.Errorf("fetch pull request files %s/%s#%d: %w", owner, name, req.PRID, err)
	}
	if len(files) == 0 {
		return UnitTestResponse{Success: true}, nil
	}

	// Build a diff context for the LLM.
	var diffParts []string
	for _, f := range files {
		diffParts = append(diffParts, fmt.Sprintf("File: %s (status: %s)\n%s", f.Path, f.Status, f.Patch))
	}

	prompt := fmt.Sprintf(
		"Generate comprehensive unit tests for the following pull request.\n"+
			"PR #%d: %s\nBase: %s, Head: %s\n\nChanged files:\n%s\n\n"+
			"Write test functions that cover the changed code paths. Return only the test code.",
		pr.Number, pr.Title, pr.Base, pr.Head, strings.Join(diffParts, "\n\n"),
	)

	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a Go/Python/JavaScript unit test generator. Write thorough, well-structured tests.",
		UserPrompt:   prompt,
		Temperature:  0.2,
		MaxTokens:    4096,
	})
	if err != nil {
		return UnitTestResponse{}, fmt.Errorf("generate unit tests: %w", err)
	}

	return UnitTestResponse{Success: true, Tests: text}, nil
}
