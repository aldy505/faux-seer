// Package handler contains HTTP handlers for faux-seer endpoints.
package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/aldy505/faux-seer/internal/auth"
	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/severity"
	"github.com/aldy505/faux-seer/internal/similarity"
)

// Server wires the HTTP layer to application services.
type Server struct {
	cfg          *config.Config
	log          *slog.Logger
	store        *db.Store
	autofix      *autofix.Service
	similarity   *similarity.Service
	severity     *severity.Service
	issueSummary *issuesummary.Service
}

// New creates a server instance.
func New(cfg *config.Config, logger *slog.Logger, store *db.Store, autofixService *autofix.Service, similarityService *similarity.Service, severityService *severity.Service, issueSummaryService *issuesummary.Service) *Server {
	return &Server{
		cfg:          cfg,
		log:          logger,
		store:        store,
		autofix:      autofixService,
		similarity:   similarityService,
		severity:     severityService,
		issueSummary: issueSummaryService,
	}
}

// Routes constructs the application's ServeMux.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /health/live", s.health)
	mux.HandleFunc("GET /health/ready", s.health)
	mux.HandleFunc("POST /v1/automation/autofix/start", s.withAuth(s.autofixStart))
	mux.HandleFunc("POST /v1/automation/autofix/update", s.withAuth(s.autofixUpdate))
	mux.HandleFunc("POST /v1/automation/autofix/state", s.withAuth(s.autofixState))
	mux.HandleFunc("POST /v1/automation/autofix/state/pr", s.withAuth(s.autofixStatePR))
	mux.HandleFunc("POST /v1/automation/autofix/prompt", s.withAuth(s.autofixPrompt))
	mux.HandleFunc("POST /v1/automation/autofix/coding-agent/state/set", s.withAuth(s.codingAgentStateSet))
	mux.HandleFunc("POST /v1/automation/autofix/coding-agent/state/update", s.withAuth(s.codingAgentStateUpdate))
	mux.HandleFunc("POST /v1/automation/codebase/repo/check-access", s.withAuth(s.repoAccess))
	mux.HandleFunc("POST /v1/automation/summarize/issue", s.withAuth(s.summarizeIssue))
	mux.HandleFunc("POST /v1/automation/summarize/trace", s.withAuth(s.summarizeTrace))
	mux.HandleFunc("POST /v1/automation/summarize/fixability", s.withAuth(s.fixability))
	mux.HandleFunc("POST /v1/project-preference", s.withAuth(s.getProjectPreference))
	mux.HandleFunc("POST /v1/project-preference/set", s.withAuth(s.setProjectPreference))
	mux.HandleFunc("POST /v1/project-preference/bulk", s.withAuth(s.bulkGetProjectPreferences))
	mux.HandleFunc("POST /v1/project-preference/bulk-set", s.withAuth(s.bulkSetProjectPreferences))
	mux.HandleFunc("POST /v1/project-preference/remove-repository", s.withAuth(s.removeRepository))
	mux.HandleFunc("POST /v0/issues/similar-issues", s.withAuth(s.similarIssues))
	mux.HandleFunc("POST /v0/issues/similar-issues/grouping-record", s.withAuth(s.groupingRecord))
	mux.HandleFunc("GET /v0/issues/similar-issues/grouping-record/delete/{project_id}", s.withAuth(s.deleteGroupingProject))
	mux.HandleFunc("POST /v0/issues/similar-issues/grouping-record/delete-by-hash", s.withAuth(s.deleteGroupingByHash))
	mux.HandleFunc("POST /v0/issues/supergroups", s.withAuth(s.supergroupUpsert))
	mux.HandleFunc("POST /v0/issues/supergroups/list", s.withAuth(s.supergroupList))
	mux.HandleFunc("POST /v0/issues/supergroups/get", s.withAuth(s.supergroupList))
	mux.HandleFunc("POST /v0/issues/supergroups/get-by-group-ids", s.withAuth(s.supergroupList))
	mux.HandleFunc("POST /v0/issues/severity-score", s.withAuth(s.severityScore))
	mux.HandleFunc("POST /v1/issues/severity-score", s.withAuth(s.severityScore))
	return mux
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, []byte)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %v", err))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err := auth.VerifyRequest(body, r.Header.Get("Authorization"), s.cfg.SharedSecrets); err != nil {
			s.writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r, body)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) repoAccess(w http.ResponseWriter, _ *http.Request, body []byte) {
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	s.writeJSON(w, http.StatusOK, map[string]bool{"has_access": true})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		s.log.Error("write-json", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

func readJSONMap(body []byte) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(body, &out)
	if out == nil {
		return map[string]any{}
	}
	return out
}

func asInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func requestContext(r *http.Request) context.Context { return r.Context() }
