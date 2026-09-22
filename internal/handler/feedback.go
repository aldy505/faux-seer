package handler

import (
	"net/http"
)

// feedbackSpamDetection asks the LLM whether a feedback message is spam.
// Consumer: feedback/usecases/spam_detection.py — reads response_data["is_spam"]
// as a bool; a missing or non-bool value is treated as failure.
func (s *Server) feedbackSpamDetection(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.feedback.SpamDetection(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// feedbackLabels asks the LLM to generate category labels. Consumer:
// feedback/usecases/label_generation.py — reads response.json()["data"]["labels"]
// as a list of strings.
func (s *Server) feedbackLabels(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.feedback.Labels(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// feedbackTitle generates a short title. Consumer:
// feedback/usecases/title_generation.py:92 — reads response.json()["title"]
// and calls .strip().
func (s *Server) feedbackTitle(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.feedback.Title(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// feedbackLabelGroups groups labels with associated labels. Consumer:
// feedback/endpoints/organization_feedback_categories.py:213 —
// reads response.json()["data"] and iterates each entry for
// primaryLabel and associatedLabels.
func (s *Server) feedbackLabelGroups(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.feedback.LabelGroups(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}

// feedbackSummarize summarizes a list of feedback messages. Consumer:
// feedback/endpoints/organization_feedback_summary.py:64 —
// reads response.json()["data"] as a free-form string.
func (s *Server) feedbackSummarize(w http.ResponseWriter, r *http.Request, body []byte) {
	response, err := s.feedback.Summarize(requestContext(r), body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, response)
}
