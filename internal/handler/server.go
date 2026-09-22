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

	"github.com/aldy505/faux-seer/internal/assistedquery"
	"github.com/aldy505/faux-seer/internal/auth"
	"github.com/aldy505/faux-seer/internal/autofix"
	"github.com/aldy505/faux-seer/internal/codeindex"
	"github.com/aldy505/faux-seer/internal/config"
	"github.com/aldy505/faux-seer/internal/db"
	"github.com/aldy505/faux-seer/internal/explorer"
	"github.com/aldy505/faux-seer/internal/feedback"
	"github.com/aldy505/faux-seer/internal/generation"
	"github.com/aldy505/faux-seer/internal/git"
	"github.com/aldy505/faux-seer/internal/investigations"
	issuesummary "github.com/aldy505/faux-seer/internal/issueSummary"
	"github.com/aldy505/faux-seer/internal/llm"
	"github.com/aldy505/faux-seer/internal/monitoring"
	"github.com/aldy505/faux-seer/internal/preferences"
	"github.com/aldy505/faux-seer/internal/replay"
	"github.com/aldy505/faux-seer/internal/runmgr"
	"github.com/aldy505/faux-seer/internal/runs"
	"github.com/aldy505/faux-seer/internal/severity"
	"github.com/aldy505/faux-seer/internal/similarity"
)

// Services carries every dependency the HTTP layer delegates to.
type Services struct {
	Config         *config.Config
	Logger         *slog.Logger
	Store          *db.Store
	LLM            llm.Client
	Git            git.Provider
	CodeIndex      *codeindex.Service
	Runs           *runmgr.Manager
	Autofix        *autofix.Service
	Explorer       *explorer.Service
	Similarity     *similarity.Service
	Severity       *severity.Service
	IssueSummary   *issuesummary.Service
	Feedback       *feedback.Service
	Replay         *replay.Service
	Generation     *generation.Service
	RunStore       *runs.Service
	Investigations *investigations.Service
	AssistedQuery  *assistedquery.Service
	Preferences    *preferences.Service
	Monitoring     *monitoring.Service
}

// Server wires the HTTP layer to application services.
type Server struct {
	cfg            *config.Config
	log            *slog.Logger
	store          *db.Store
	llm            llm.Client
	git            git.Provider
	codeIndex      *codeindex.Service
	runs           *runmgr.Manager
	autofix        *autofix.Service
	explorer       *explorer.Service
	similarity     *similarity.Service
	severity       *severity.Service
	issueSummary   *issuesummary.Service
	feedback       *feedback.Service
	replay         *replay.Service
	generation     *generation.Service
	runStore       *runs.Service
	investigations *investigations.Service
	assistedQuery  *assistedquery.Service
	preferences    *preferences.Service
	monitoring     *monitoring.Service
}

// New creates a server instance.
func New(services Services) *Server {
	logger := services.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:            services.Config,
		log:            logger,
		store:          services.Store,
		llm:            services.LLM,
		git:            services.Git,
		codeIndex:      services.CodeIndex,
		runs:           services.Runs,
		autofix:        services.Autofix,
		explorer:       services.Explorer,
		similarity:     services.Similarity,
		severity:       services.Severity,
		issueSummary:   services.IssueSummary,
		feedback:       services.Feedback,
		replay:         services.Replay,
		generation:     services.Generation,
		runStore:       services.RunStore,
		investigations: services.Investigations,
		assistedQuery:  services.AssistedQuery,
		preferences:    services.Preferences,
		monitoring:     services.Monitoring,
	}
}

// Routes constructs the application's ServeMux.
//
// The route table mirrors every path Sentry's signed Seer client can issue
// (see src/sentry/seer/signed_seer_api.py and the sibling seer client modules
// in the Sentry tree). Paths Sentry never sends are not served.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /health/live", s.health)
	mux.HandleFunc("GET /health/ready", s.health)

	// Agent and explorer runs.
	mux.HandleFunc("POST /v1/automation/agent/feature/run", s.withAuth(s.agentFeatureRun))
	mux.HandleFunc("POST /v1/automation/explorer/chat", s.withAuth(s.explorerChat))
	mux.HandleFunc("POST /v1/automation/explorer/state", s.withAuth(s.explorerState))
	mux.HandleFunc("POST /v1/automation/explorer/state/pr", s.withAuth(s.explorerStatePR))
	mux.HandleFunc("POST /v1/automation/explorer/runs/by-ids", s.withAuth(s.explorerRunsByIDs))
	mux.HandleFunc("POST /v1/automation/explorer/repos", s.withAuth(s.explorerRepos))
	mux.HandleFunc("POST /v1/automation/explorer/update", s.withAuth(s.explorerUpdate))
	mux.HandleFunc("POST /v1/automation/explorer/index", s.withAuth(s.explorerIndexGeneric))
	mux.HandleFunc("POST /v1/automation/explorer/index/org-repo-knowledge", s.withAuth(s.explorerIndexOrgRepo))
	mux.HandleFunc("POST /v1/automation/explorer/index/org-project-knowledge", s.withAuth(s.explorerIndexProject))
	mux.HandleFunc("POST /v1/automation/explorer/index/sentry-knowledge", s.withAuth(s.explorerIndexSentryKnowledge))
	mux.HandleFunc("POST /v1/automation/explorer/export-indexes", s.withAuth(s.explorerExportIndexes))
	mux.HandleFunc("POST /v1/explorer/service-map/update", s.withAuth(s.serviceMapUpdate))

	// Autofix coding-agent state.
	mux.HandleFunc("POST /v1/automation/autofix/coding-agent/state/set", s.withAuth(s.codingAgentStateSet))
	mux.HandleFunc("POST /v1/automation/autofix/coding-agent/state/update", s.withAuth(s.codingAgentStateUpdate))

	// Summaries: issues, traces, feedback, replays.
	mux.HandleFunc("POST /v1/automation/summarize/issue", s.withAuth(s.summarizeIssue))
	mux.HandleFunc("POST /v1/automation/summarize/trace", s.withAuth(s.summarizeTrace))
	mux.HandleFunc("POST /v1/automation/summarize/fixability", s.withAuth(s.fixability))
	mux.HandleFunc("POST /v1/automation/summarize/feedback/spam-detection", s.withAuth(s.feedbackSpamDetection))
	mux.HandleFunc("POST /v1/automation/summarize/feedback/labels", s.withAuth(s.feedbackLabels))
	mux.HandleFunc("POST /v1/automation/summarize/feedback/title", s.withAuth(s.feedbackTitle))
	mux.HandleFunc("POST /v1/automation/summarize/feedback/label-groups", s.withAuth(s.feedbackLabelGroups))
	mux.HandleFunc("POST /v1/automation/summarize/feedback/summarize", s.withAuth(s.feedbackSummarize))
	mux.HandleFunc("POST /v1/automation/summarize/replay/breadcrumbs/start", s.withAuth(s.replayBreadcrumbsStart))
	mux.HandleFunc("POST /v1/automation/summarize/replay/breadcrumbs/state", s.withAuth(s.replayBreadcrumbsState))
	mux.HandleFunc("POST /v1/automation/summarize/replay/breadcrumbs/delete", s.withAuth(s.replayBreadcrumbsDelete))

	// Investigations.
	mux.HandleFunc("POST /v1/automation/investigations", s.withAuth(s.investigationCreate))
	mux.HandleFunc("POST /v1/automation/investigations/{run_id}/commands", s.withAuth(s.investigationCommand))
	mux.HandleFunc("GET /v1/automation/investigations/{run_id}", s.withAuth(s.investigationGet))

	// Issue detection.
	mux.HandleFunc("POST /v1/automation/issue-detection/analyze", s.withAuth(s.issueDetectionAnalyze))
	mux.HandleFunc("GET /v1/automation/issue-detection/check-budget/{org_id}", s.withAuth(s.issueDetectionCheckBudget))

	// Codegen and assisted query.
	mux.HandleFunc("POST /v1/automation/codegen/unit-tests", s.withAuth(s.unitTestsGenerate))
	mux.HandleFunc("POST /v1/assisted-query/state", s.withAuth(s.assistedQueryState))
	mux.HandleFunc("POST /v1/assisted-query/start", s.withAuth(s.assistedQueryStart))
	mux.HandleFunc("POST /v1/assisted-query/translate", s.withAuth(s.assistedQueryTranslate))
	mux.HandleFunc("POST /v1/assisted-query/translate-agentic", s.withAuth(s.assistedQueryTranslateAgentic))
	mux.HandleFunc("POST /v1/assisted-query/create-cache", s.withAuth(s.assistedQueryCreateCache))

	// Anomaly detection, breakpoints, workflows.
	mux.HandleFunc("POST /v1/anomaly-detection/detect", s.withAuth(s.anomalyDetect))
	mux.HandleFunc("POST /v1/anomaly-detection/alert-data", s.withAuth(s.anomalyAlertData))
	mux.HandleFunc("POST /v1/anomaly-detection/store", s.withAuth(s.anomalyStore))
	mux.HandleFunc("POST /v1/anomaly-detection/delete-alert-data", s.withAuth(s.anomalyDeleteAlertData))
	mux.HandleFunc("POST /v1/workflows/compare/cohort", s.withAuth(s.workflowCompareCohort))
	mux.HandleFunc("POST /trends/breakpoint-detector", s.withAuth(s.breakpointDetector))

	// Code review, offboarding, PR metrics.
	mux.HandleFunc("POST /v1/code_review/check/rerun", s.withAuth(s.codeReviewRerun))
	mux.HandleFunc("POST /v1/code_review/review-request", s.withAuth(s.codeReviewRequest))
	mux.HandleFunc("POST /v1/code_review/pr-closed", s.withAuth(s.codeReviewPRClosed))
	mux.HandleFunc("POST /v1/offboarding/repository", s.withAuth(s.offboardRepository))
	mux.HandleFunc("POST /v1/pr-metrics/delegated-agent-match", s.withAuth(s.prMetricsDelegatedAgentMatch))
	mux.HandleFunc("POST /v1/pr-metrics/pr-close-judge", s.withAuth(s.prMetricsPRCloseJudge))

	// Grouping, supergroups, severity, models, monitoring providers.
	mux.HandleFunc("POST /v0/issues/similar-issues", s.withAuth(s.similarIssues))
	mux.HandleFunc("GET /v0/issues/similar-issues/grouping-record/delete/{project_id}", s.withAuth(s.deleteGroupingProject))
	mux.HandleFunc("POST /v0/issues/similar-issues/grouping-record/delete-by-hash", s.withAuth(s.deleteGroupingByHash))
	mux.HandleFunc("POST /v0/issues/supergroups/cluster-lightweight", s.withAuth(s.supergroupClusterLightweight))
	mux.HandleFunc("POST /v0/issues/supergroups/get", s.withAuth(s.supergroupList))
	mux.HandleFunc("POST /v0/issues/supergroups/get-by-group-ids", s.withAuth(s.supergroupList))
	mux.HandleFunc("POST /v0/issues/severity-score", s.withAuth(s.severityScore))
	mux.HandleFunc("GET /v1/models", s.withAuth(s.seerModels))
	mux.HandleFunc("POST /v1/llm/generate", s.withAuth(s.llmGenerate))
	mux.HandleFunc("POST /v1/automation/oneshot/run", s.withAuth(s.oneshotRun))
	mux.HandleFunc("POST /v1/monitoring-providers/gcp/verify-connection", s.withAuth(s.monitoringProviderVerifyConnection))

	// Project preference maintenance.
	mux.HandleFunc("POST /v1/project-preference/remove-repository", s.withAuth(s.projectPreferenceRemoveRepository))
	mux.HandleFunc("POST /v1/project-preference/bulk-remove-repositories", s.withAuth(s.projectPreferenceBulkRemoveRepositories))
	mux.HandleFunc("POST /v1/project-preference/remove-handoffs-for-integration", s.withAuth(s.projectPreferenceRemoveHandoffsForIntegration))

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

// decodeOptionalJSONBody decodes a request body into out, tolerating an empty
// body. Several Seer endpoints are GETs whose body is always empty; treating
// that as malformed JSON would surface as a request failure to Sentry.
func decodeOptionalJSONBody(body []byte, out any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
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
