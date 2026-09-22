// Package assistedquery implements Seer's assisted-query (trace explorer AI)
// endpoints: a background run that answers a natural-language question, and
// query translation into Sentry trace-explorer query syntax.
package assistedquery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/runs"
)

// Service orchestrates assisted-query runs and translations.
type Service struct {
	runs      *runs.Service
	codeIndex *codeindex.Service
}

// New creates an assisted-query service.
func New(runService *runs.Service, index *codeindex.Service) *Service {
	return &Service{runs: runService, codeIndex: index}
}

// StartResponse is returned when a run is started.
type StartResponse struct {
	RunID int64 `json:"run_id"`
}

// Session is the run state the Sentry frontend renders.
type Session struct {
	RunID             int64          `json:"run_id"`
	Status            string         `json:"status"`
	CurrentStep       *Step          `json:"current_step"`
	CompletedSteps    []Step         `json:"completed_steps"`
	UpdatedAt         string         `json:"updated_at"`
	FinalResponse     map[string]any `json:"final_response"`
	UnsupportedReason *string        `json:"unsupported_reason"`
}

// Step is one entry in the frontend's step list.
type Step struct {
	Key string `json:"key"`
}

// StateResponse wraps the session.
type StateResponse struct {
	Session Session `json:"session"`
}

// TranslateResponse holds translated queries.
type TranslateResponse struct {
	Responses         []Query `json:"responses"`
	UnsupportedReason *string `json:"unsupported_reason"`
}

// Query is one translated trace-explorer query. Sentry reads every field except
// Mode; GroupBy and Visualization must be arrays when present.
type Query struct {
	Query         string   `json:"query"`
	StatsPeriod   string   `json:"stats_period"`
	GroupBy       []string `json:"group_by,omitempty"`
	Visualization []string `json:"visualization,omitempty"`
	Sort          string   `json:"sort"`
	Mode          string   `json:"mode,omitempty"`
}

type startRequest struct {
	OrgID                  int64          `json:"org_id"`
	OrgSlug                string         `json:"org_slug"`
	ProjectIDs             []int64        `json:"project_ids"`
	NaturalLanguageQuery   string         `json:"natural_language_query"`
	Strategy               string         `json:"strategy"`
	ExternalIdempotencyKey string         `json:"external_idempotency_key"`
	Options                map[string]any `json:"options"`
}

type stateRequest struct {
	RunID          int64 `json:"run_id"`
	OrganizationID int64 `json:"organization_id"`
}

type translateRequest struct {
	OrgID                int64   `json:"org_id"`
	OrgSlug              string  `json:"org_slug"`
	ProjectIDs           []int64 `json:"project_ids"`
	NaturalLanguageQuery string  `json:"natural_language_query"`
	Strategy             string  `json:"strategy"`
}

// Start launches a background assisted-query run and returns its id.
func (s *Service) Start(ctx context.Context, raw json.RawMessage) (StartResponse, error) {
	var request startRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return StartResponse{}, fmt.Errorf("decode assisted-query start request: %w", err)
	}
	query := strings.TrimSpace(request.NaturalLanguageQuery)
	if query == "" {
		return StartResponse{}, fmt.Errorf("natural_language_query is required")
	}
	if request.ExternalIdempotencyKey != "" {
		existing, err := s.runs.FindByIdempotencyKey(ctx, runs.KindAssistedQuery, request.ExternalIdempotencyKey)
		if err != nil {
			return StartResponse{}, err
		}
		if existing != nil {
			return StartResponse{RunID: existing.ID}, nil
		}
	}
	organizationID := request.OrgID
	record := db.RunRecord{
		Kind:           runs.KindAssistedQuery,
		OrganizationID: &organizationID,
		Status:         db.RunStatusProcessing,
		RequestJSON:    raw,
	}
	if request.ExternalIdempotencyKey != "" {
		key := request.ExternalIdempotencyKey
		record.IdempotencyKey = &key
	}
	run, err := s.runs.Start(ctx, record, s.answerTask(request))
	if err != nil {
		return StartResponse{}, err
	}
	return StartResponse{RunID: run.ID}, nil
}

// State reports the run state the frontend polls.
func (s *Service) State(ctx context.Context, raw json.RawMessage) (StateResponse, error) {
	var request stateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return StateResponse{}, fmt.Errorf("decode assisted-query state request: %w", err)
	}
	record, err := s.runs.Get(ctx, request.RunID)
	if err != nil {
		return StateResponse{}, err
	}
	session := Session{
		RunID:          request.RunID,
		Status:         db.RunStatusError,
		CompletedSteps: []Step{},
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	if record == nil {
		reason := "run not found"
		session.UnsupportedReason = &reason
		return StateResponse{Session: session}, nil
	}
	session.Status = s.runs.Status(record)
	session.UpdatedAt = record.UpdatedAt
	switch session.Status {
	case db.RunStatusCompleted:
		session.FinalResponse = s.runs.Projection(record)
	case db.RunStatusError:
		reason := "run failed"
		if record.Error != nil {
			reason = *record.Error
		}
		session.UnsupportedReason = &reason
	}
	// faux-seer's runs expose no intermediate step log, and Sentry's frontend
	// renders step keys from its own catalog, so the list stays empty.
	return StateResponse{Session: session}, nil
}

// Translate converts a natural-language question into trace-explorer queries.
func (s *Service) Translate(ctx context.Context, raw json.RawMessage) (TranslateResponse, error) {
	var request translateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return TranslateResponse{}, fmt.Errorf("decode assisted-query translate request: %w", err)
	}
	return s.translate(ctx, request)
}

// TranslateAgentic is the agentic variant of Translate. faux-seer uses the same
// translation, with the code index supplying repository context when available.
func (s *Service) TranslateAgentic(ctx context.Context, raw json.RawMessage) (TranslateResponse, error) {
	var request translateRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return TranslateResponse{}, fmt.Errorf("decode assisted-query translate-agentic request: %w", err)
	}
	return s.translate(ctx, request)
}

func (s *Service) translate(ctx context.Context, request translateRequest) (TranslateResponse, error) {
	query := strings.TrimSpace(request.NaturalLanguageQuery)
	if query == "" {
		return TranslateResponse{}, fmt.Errorf("natural_language_query is required")
	}
	prompt := translatePrompt(query, request.ProjectIDs) + s.codeContext(ctx, request.OrgID, query)
	answer, err := s.runs.CompleteText(ctx, translateSystemPrompt, prompt, 800)
	if err != nil {
		return TranslateResponse{}, err
	}
	responses, unsupported := parseTranslatedQueries(answer)
	return TranslateResponse{Responses: responses, UnsupportedReason: unsupported}, nil
}

// answerTask answers the run's question, grounded in indexed code when the
// organization has an index.
func (s *Service) answerTask(request startRequest) runs.Task {
	return func(ctx context.Context, runID int64) error {
		prompt := fmt.Sprintf("Question: %s\nProjects: %v\nSearch strategy: %s", strings.TrimSpace(request.NaturalLanguageQuery), request.ProjectIDs, request.Strategy)
		prompt += s.codeContext(ctx, request.OrgID, request.NaturalLanguageQuery)
		answer, err := s.runs.CompleteText(ctx, answerSystemPrompt, prompt, 900)
		if err != nil {
			return s.runs.Fail(ctx, runID, err)
		}
		return s.runs.Complete(ctx, runID, map[string]any{"answer": answer})
	}
}

// codeContext retrieves indexed repository chunks for a question.
func (s *Service) codeContext(ctx context.Context, organizationID int64, query string) string {
	if s.codeIndex == nil || organizationID == 0 || strings.TrimSpace(query) == "" {
		return ""
	}
	hasIndex, err := s.codeIndex.HasOrgIndex(ctx, organizationID)
	if err != nil || !hasIndex {
		return ""
	}
	results, err := s.codeIndex.Search(ctx, query, codeindex.SearchOptions{OrganizationID: organizationID, K: 3})
	if err != nil || len(results) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("\n\nIndexed repository code that may be relevant:\n")
	for _, result := range results {
		builder.WriteString(fmt.Sprintf("\n%s:%d-%d\n%s\n", result.Path, result.StartLine, result.EndLine, strings.TrimSpace(result.Text)))
	}
	return builder.String()
}

const translateSystemPrompt = `You translate natural-language questions about a Sentry trace dataset into trace-explorer queries.
Reply with JSON only, shaped exactly like:
{"responses":[{"query":"<query>","stats_period":"<period>","group_by":[],"visualization":[],"sort":"<sort>","mode":"spans"}],"unsupported_reason":null}
"query" uses Sentry search syntax over trace attributes (for example "span.op:db transaction:/api/*/checkout").
"stats_period" is a relative window such as "14d". "group_by" and "visualization" are arrays of attribute or visual names.
"sort" is a field name with an optional leading "-" for descending. "mode" is "spans", "aggregate", or "samples".
Return an empty responses list and a short "unsupported_reason" string when the question cannot be expressed as a trace query.`

const answerSystemPrompt = `You are a Sentry trace-explorer search agent. Answer the user's question about their trace data as precisely as the available context allows, and state clearly when the context is insufficient.`

// translatePrompt renders the user's question for the translation model.
func translatePrompt(query string, projectIDs []int64) string {
	return fmt.Sprintf("Question: %s\nProjects: %v", query, projectIDs)
}

// translatedEnvelope is the JSON object the translation model returns.
type translatedEnvelope struct {
	Responses []struct {
		Query         string   `json:"query"`
		StatsPeriod   string   `json:"stats_period"`
		GroupBy       []string `json:"group_by"`
		Visualization []string `json:"visualization"`
		Sort          string   `json:"sort"`
		Mode          string   `json:"mode"`
	} `json:"responses"`
	UnsupportedReason *string `json:"unsupported_reason"`
}

// parseTranslatedQueries decodes the model's JSON and normalizes it into the
// shape Sentry indexes into: every returned entry has a query, a stats period,
// and a sort, and an entry without a query is dropped rather than shipped.
func parseTranslatedQueries(answer string) ([]Query, *string) {
	var envelope translatedEnvelope
	if !llm.ExtractJSON(answer, &envelope) {
		reason := "the model did not return a usable query"
		return []Query{}, &reason
	}
	responses := make([]Query, 0, len(envelope.Responses))
	for _, entry := range envelope.Responses {
		query := strings.TrimSpace(entry.Query)
		if query == "" {
			continue
		}
		translated := Query{
			Query:         query,
			StatsPeriod:   strings.TrimSpace(entry.StatsPeriod),
			GroupBy:       entry.GroupBy,
			Visualization: entry.Visualization,
			Sort:          strings.TrimSpace(entry.Sort),
			Mode:          strings.TrimSpace(entry.Mode),
		}
		if translated.StatsPeriod == "" {
			translated.StatsPeriod = "14d"
		}
		if translated.Sort == "" {
			translated.Sort = "-timestamp"
		}
		if translated.Mode == "" {
			translated.Mode = "spans"
		}
		responses = append(responses, translated)
	}
	if len(responses) == 0 && envelope.UnsupportedReason == nil {
		reason := "no trace query matched the question"
		return responses, &reason
	}
	return responses, envelope.UnsupportedReason
}
