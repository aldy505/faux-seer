// Package feedback implements LLM-backed user-feedback summarization
// endpoints that Sentry's feedback ingestion pipeline consumes.
package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aldy505/faux-seer/internal/llm"
)

// Service generates feedback labels, titles, summaries, and spam verdicts.
type Service struct{ llm llm.Client }

// New creates a feedback service.
func New(llmClient llm.Client) *Service { return &Service{llm: llmClient} }

// SpamDetectionRequest matches Sentry's SpamDetectionRequest TypedDict
// (feedback/lib/seer_api.py:16).
type SpamDetectionRequest struct {
	OrganizationID  int64  `json:"organization_id"`
	FeedbackMessage string `json:"feedback_message"`
}

// LabelGenerationRequest matches Sentry's LabelGenerationRequest TypedDict
// (feedback/lib/seer_api.py:21).
type LabelGenerationRequest struct {
	OrganizationID  int64  `json:"organization_id"`
	FeedbackMessage string `json:"feedback_message"`
}

// GenerateFeedbackTitleRequest matches Sentry's GenerateFeedbackTitleRequest
// TypedDict (feedback/lib/seer_api.py:26).
type GenerateFeedbackTitleRequest struct {
	OrganizationID  int64  `json:"organization_id"`
	FeedbackMessage string `json:"feedback_message"`
}

// LabelGroupFeedbacksContext is one feedback entry with its labels, used by
// the label-groups endpoint (feedback/lib/seer_api.py:31).
type LabelGroupFeedbacksContext struct {
	Feedback string   `json:"feedback"`
	Labels   []string `json:"labels"`
}

// LabelGroupsRequest matches Sentry's LabelGroupsRequest TypedDict
// (feedback/lib/seer_api.py:36).
type LabelGroupsRequest struct {
	Labels           []string                     `json:"labels"`
	FeedbacksContext []LabelGroupFeedbacksContext `json:"feedbacks_context"`
}

// SummarizeFeedbacksRequest matches Sentry's SummarizeFeedbacksRequest
// TypedDict (feedback/lib/seer_api.py:41).
type SummarizeFeedbacksRequest struct {
	Feedbacks []string `json:"feedbacks"`
}

// SpamDetectionResponse is {"is_spam": bool}.
// Consumer: feedback/usecases/spam_detection.py:54 — response_data["is_spam"].
type SpamDetectionResponse struct {
	IsSpam bool `json:"is_spam"`
}

// LabelsResponse is {"data": {"labels": []string}}.
// Consumer: feedback/usecases/label_generation.py:55 — response.json()["data"]["labels"].
type LabelsResponse struct {
	Data struct {
		Labels []string `json:"labels"`
	} `json:"data"`
}

// TitleResponse is {"title": string}.
// Consumer: feedback/usecases/title_generation.py:92 — response.json()["title"].
type TitleResponse struct {
	Title string `json:"title"`
}

// LabelGroupEntry is one primary label with associated labels.
type LabelGroupEntry struct {
	PrimaryLabel     string   `json:"primaryLabel"`
	AssociatedLabels []string `json:"associatedLabels"`
}

// LabelGroupsResponse is {"data": [LabelGroupEntry]}.
// Consumer: feedback/endpoints/organization_feedback_categories.py:213
// — response.json()["data"].
type LabelGroupsResponse struct {
	Data []LabelGroupEntry `json:"data"`
}

// SummarizeResponse is {"data": string}.
// Consumer: feedback/endpoints/organization_feedback_summary.py:64
// — response.json()["data"].
type SummarizeResponse struct {
	Data string `json:"data"`
}

// SpamDetection asks the LLM whether a feedback message is spam.
func (s *Service) SpamDetection(ctx context.Context, raw json.RawMessage) (SpamDetectionResponse, error) {
	var req SpamDetectionRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return SpamDetectionResponse{}, fmt.Errorf("decode spam detection request: %w", err)
	}
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a spam classifier. Reply with exactly one JSON object: {\"is_spam\": <true or false>}. No other text.",
		UserPrompt:   fmt.Sprintf("Is the following user feedback spam?\n\n%s", req.FeedbackMessage),
		Temperature:  0.0,
		MaxTokens:    64,
	})
	if err != nil {
		return SpamDetectionResponse{IsSpam: false}, fmt.Errorf("spam detection LLM call: %w", err)
	}
	var result struct {
		IsSpam *bool `json:"is_spam"`
	}
	if llm.ExtractJSON(text, &result) && result.IsSpam != nil {
		return SpamDetectionResponse{IsSpam: *result.IsSpam}, nil
	}
	// Fallback: not spam.
	return SpamDetectionResponse{IsSpam: false}, nil
}

// Labels asks the LLM to generate category labels for a feedback message.
func (s *Service) Labels(ctx context.Context, raw json.RawMessage) (LabelsResponse, error) {
	var req LabelGenerationRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return LabelsResponse{}, fmt.Errorf("decode label generation request: %w", err)
	}
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a feedback classifier. Reply with exactly one JSON object: {\"labels\": [\"<short lowercase category>\", ...]}. Use short lowercase category names. No other text.",
		UserPrompt:   fmt.Sprintf("Classify this user feedback into categories:\n\n%s", req.FeedbackMessage),
		Temperature:  0.1,
		MaxTokens:    256,
	})
	if err != nil {
		return LabelsResponse{}, fmt.Errorf("label generation LLM call: %w", err)
	}
	var result struct {
		Labels []string `json:"labels"`
	}
	if llm.ExtractJSON(text, &result) && len(result.Labels) > 0 {
		resp := LabelsResponse{}
		resp.Data.Labels = result.Labels
		return resp, nil
	}
	// Fallback: empty labels.
	resp := LabelsResponse{}
	resp.Data.Labels = []string{}
	return resp, nil
}

// Title derives a short title from a feedback message, using the LLM when
// possible and falling back to the deterministic heuristic.
func (s *Service) Title(ctx context.Context, raw json.RawMessage) (TitleResponse, error) {
	var req GenerateFeedbackTitleRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return TitleResponse{}, fmt.Errorf("decode title generation request: %w", err)
	}
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a title generator. Reply with exactly one JSON object: {\"title\": \"<short descriptive title>\"}. No other text. The title must be under 60 characters.",
		UserPrompt:   fmt.Sprintf("Generate a short title for this user feedback:\n\n%s", req.FeedbackMessage),
		Temperature:  0.1,
		MaxTokens:    64,
	})
	if err == nil {
		var result struct {
			Title string `json:"title"`
		}
		if llm.ExtractJSON(text, &result) && strings.TrimSpace(result.Title) != "" {
			return TitleResponse{Title: truncateTitle(result.Title)}, nil
		}
	}
	// Fallback: heuristic title from the first non-empty line.
	return TitleResponse{Title: DeriveFeedbackTitle(req.FeedbackMessage)}, nil
}

// LabelGroups asks the LLM to group labels with associated labels.
func (s *Service) LabelGroups(ctx context.Context, raw json.RawMessage) (LabelGroupsResponse, error) {
	var req LabelGroupsRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return LabelGroupsResponse{}, fmt.Errorf("decode label groups request: %w", err)
	}
	var contextDesc string
	if len(req.FeedbacksContext) > 0 {
		var parts []string
		for _, fc := range req.FeedbacksContext {
			parts = append(parts, fmt.Sprintf("- Feedback: %q (labels: %v)", fc.Feedback, fc.Labels))
		}
		contextDesc = fmt.Sprintf("\n\nFeedback context:\n%s", strings.Join(parts, "\n"))
	}
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You are a label grouper. Reply with exactly one JSON array of objects, each with \"primaryLabel\" (string) and \"associatedLabels\" (array of strings). No other text.",
		UserPrompt:   fmt.Sprintf("Group these labels with related associated labels:%s\n\nLabels: %v", contextDesc, req.Labels),
		Temperature:  0.1,
		MaxTokens:    512,
	})
	if err != nil {
		return LabelGroupsResponse{}, fmt.Errorf("label groups LLM call: %w", err)
	}
	var groups []LabelGroupEntry
	if llm.ExtractJSON(text, &groups) && len(groups) > 0 {
		// Ensure every entry has non-nil AssociatedLabels.
		for i := range groups {
			if groups[i].AssociatedLabels == nil {
				groups[i].AssociatedLabels = []string{}
			}
		}
		return LabelGroupsResponse{Data: groups}, nil
	}
	// Fallback: each label as its own primary with empty associated.
	groups = make([]LabelGroupEntry, 0, len(req.Labels))
	for _, label := range req.Labels {
		groups = append(groups, LabelGroupEntry{
			PrimaryLabel:     label,
			AssociatedLabels: []string{},
		})
	}
	return LabelGroupsResponse{Data: groups}, nil
}

// Summarize asks the LLM to summarize a list of feedback messages.
func (s *Service) Summarize(ctx context.Context, raw json.RawMessage) (SummarizeResponse, error) {
	var req SummarizeFeedbacksRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return SummarizeResponse{}, fmt.Errorf("decode summarize feedback request: %w", err)
	}
	var feedbackDesc string
	if len(req.Feedbacks) > 0 {
		var parts []string
		for i, fb := range req.Feedbacks {
			parts = append(parts, fmt.Sprintf("%d. %s", i+1, fb))
		}
		feedbackDesc = strings.Join(parts, "\n")
	} else {
		feedbackDesc = "(no feedbacks provided)"
	}
	text, err := s.llm.Complete(ctx, llm.CompletionRequest{
		SystemPrompt: "You summarize user feedback. Reply with exactly one JSON object: {\"data\": \"<summary of the feedback>\"}. No other text.",
		UserPrompt:   fmt.Sprintf("Summarize these user feedbacks:\n\n%s", feedbackDesc),
		Temperature:  0.1,
		MaxTokens:    512,
	})
	if err != nil {
		return SummarizeResponse{}, fmt.Errorf("summarize feedback LLM call: %w", err)
	}
	var result struct {
		Data string `json:"data"`
	}
	if llm.ExtractJSON(text, &result) && strings.TrimSpace(result.Data) != "" {
		return SummarizeResponse{Data: result.Data}, nil
	}
	// Fallback: deterministic summary.
	return SummarizeResponse{Data: fmt.Sprintf("Compatibility summary of %d user feedback items.", len(req.Feedbacks))}, nil
}

// DeriveFeedbackTitle returns the first non-empty trimmed line of msg
// truncated to 60 runes, or "User feedback" when the message is absent or
// empty after trimming. This is the deterministic heuristic fallback used
// when the LLM is unavailable or returns an unparseable result.
func DeriveFeedbackTitle(msg string) string {
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > 60 {
			runes := []rune(line)
			line = string(runes[:60])
		}
		return line
	}
	return "User feedback"
}

func truncateTitle(title string) string {
	title = strings.TrimSpace(title)
	if utf8.RuneCountInString(title) > 60 {
		runes := []rune(title)
		title = string(runes[:60])
	}
	return title
}
