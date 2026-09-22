package handler

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

// feedbackSpamDetection always reports the message as not spam. Sentry reads
// the boolean is_spam field; a missing or non-bool value is treated as failure.
func (s *Server) feedbackSpamDetection(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s.writeJSON(w, http.StatusOK, map[string]bool{"is_spam": false})
}

// feedbackLabels returns an empty label set. Sentry indexes the nested
// data.labels JSON array; a nil or missing value is treated as failure.
func (s *Server) feedbackLabels(w http.ResponseWriter, _ *http.Request, _ []byte) {
	s.writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"labels": []string{}}})
}

// feedbackTitle extracts a short title from the feedback message. Sentry
// calls .strip() on the string, so it must never be empty or whitespace-only.
func (s *Server) feedbackTitle(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		FeedbackMessage string `json:"feedback_message"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode feedback title request: %v", err))
		return
	}
	title := deriveFeedbackTitle(request.FeedbackMessage)
	s.writeJSON(w, http.StatusOK, map[string]string{"title": title})
}

// feedbackLabelGroups mirrors each requested label into an entry with
// primaryLabel and associatedLabels. Sentry iterates both keys on every entry.
func (s *Server) feedbackLabelGroups(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		Labels []string `json:"labels"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode feedback label groups request: %v", err))
		return
	}
	entries := make([]map[string]any, 0, len(request.Labels))
	for _, label := range request.Labels {
		entries = append(entries, map[string]any{
			"primaryLabel":     label,
			"associatedLabels": []string{},
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"data": entries})
}

// feedbackSummarize composes a deterministic summary sentence from the
// provided feedback list. Sentry reads data as a free-form string.
func (s *Server) feedbackSummarize(w http.ResponseWriter, r *http.Request, body []byte) {
	var request struct {
		Feedbacks []string `json:"feedbacks"`
	}
	if err := decodeOptionalJSONBody(body, &request); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("decode feedback summarize request: %v", err))
		return
	}
	count := len(request.Feedbacks)
	summary := fmt.Sprintf("Compatibility summary of %d user feedback items.", count)
	s.writeJSON(w, http.StatusOK, map[string]string{"data": summary})
}

// deriveFeedbackTitle returns the first non-empty trimmed line of msg
// truncated to 60 runes, or "User feedback" when the message is absent or
// empty after trimming.
func deriveFeedbackTitle(msg string) string {
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
