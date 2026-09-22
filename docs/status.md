# Current Status and Remaining Gaps

This document summarizes what `faux-seer` currently implements and what is still missing relative to the original compatibility-first build plan.

## What is implemented

The current codebase includes:

**HTTP surface (63 Seer paths + 3 health routes = 66 routes total):**

- Sentry-compatible HTTP auth using `Authorization: Rpcsignature rpc0:<hex>`
- 3 health endpoints (`/health`, `/health/live`, `/health/ready`)
- Agent/explorer family: `agent/feature/run`, `explorer/chat`, `explorer/state`, `explorer/state/pr`, `explorer/runs/by-ids`, `explorer/repos`, `explorer/update`, `explorer/index`, `explorer/index/org-repo-knowledge`, `explorer/index/org-project-knowledge`, `explorer/index/sentry-knowledge`, `explorer/export-indexes`, `explorer/service-map/update`
- Autofix coding-agent state: `autofix/coding-agent/state/set`, `autofix/coding-agent/state/update`
- Codegen and oneshot: `codegen/unit-tests`, `oneshot/run`
- Summaries: `summarize/issue`, `summarize/trace`, `summarize/fixability`, `summarize/feedback/spam-detection`, `summarize/feedback/labels`, `summarize/feedback/title`, `summarize/feedback/label-groups`, `summarize/feedback/summarize`, `summarize/replay/breadcrumbs/start`, `summarize/replay/breadcrumbs/state`, `summarize/replay/breadcrumbs/delete`
- Investigations: `investigations` (POST), `investigations/{run_id}/commands`, `investigations/{run_id}` (GET)
- Issue detection: `issue-detection/analyze` (202), `issue-detection/check-budget/{org_id}`
- Assisted query: `assisted-query/start`, `assisted-query/state`, `assisted-query/translate`, `assisted-query/translate-agentic`, `assisted-query/create-cache`
- Anomaly detection + breakpoints + workflows: `anomaly-detection/detect`, `anomaly-detection/alert-data`, `anomaly-detection/store`, `anomaly-detection/delete-alert-data`, `workflows/compare/cohort`, `trends/breakpoint-detector`
- Code review + offboarding + PR metrics: `code_review/check/rerun`, `code_review/review-request`, `code_review/pr-closed`, `offboarding/repository`, `pr-metrics/delegated-agent-match` (202), `pr-metrics/pr-close-judge`
- Grouping, supergroups, severity: `issues/similar-issues`, `issues/similar-issues/grouping-record/delete/{project_id}`, `issues/similar-issues/grouping-record/delete-by-hash`, `issues/supergroups/cluster-lightweight`, `issues/supergroups/get`, `issues/supergroups/get-by-group-ids`, `issues/severity-score`
- Models + LLM/generate + monitoring-provider verification: `models` (GET), `llm/generate`, `monitoring-providers/gcp/verify-connection`
- Project-preference maintenance: `project-preference/remove-repository`, `project-preference/bulk-remove-repositories`, `project-preference/remove-handoffs-for-integration`

**Persistence and storage:**

- SQLite app-state persistence (autofix runs, explorer runs, generic runs, run commands, replay breadcrumb summaries, project preferences, grouping records, supergroups)
- Real `sqlite-vec` integration for the SQLite vector backend
- Real `pgvector` integration for the advanced vector backend

**Provider layer:**

- Round-robin model selection across comma-separated `LLM_MODEL` / `EMBEDDING_MODEL` lists
- LLM clients: stub, OpenAI-compatible, Anthropic
- Embedding clients: stub, OpenAI-compatible

**Observability and deployment:**

- Sentry observability with `sentry-go` for errors, tracing, logs, and metrics
- Structured per-request access logging with method, URL, headers, body, status, and duration
- Debian trixie Docker build/runtime images

The codebase also has passing:

- `go test ./...`
- `go vet ./...`
- `go build ./...`

## What is real versus simulated

Most endpoints now perform real work when the right credentials are configured. The following remain simulated because their dependencies are internal to Seer or require infrastructure not available externally.

**Still simulated (by design):**

- `explorer/index` (generic) — the request carries only org and project ids, no repository identity
- `explorer/index/org-project-knowledge` — requires Seer-internal project metadata
- `explorer/index/sentry-knowledge` — Sentry's documentation corpus is not reachable
- `explorer/export-indexes` — export format and destination are internal to Seer
- `explorer/service-map/update` — faux-seer stores no service map
- `issue-detection/analyze` (202 ack) — requires the event-processing pipeline that feeds detection
- `issue-detection/check-budget/{org_id}` — no budget store; always returns `{"has_budget": true}`
- `anomaly-detection/*` — requires a trained timeseries model and historical data store
- `workflows/compare/cohort` — requires a metric/events backend
- `trends/breakpoint-detector` — requires a statistical timeseries backend
- `code_review/*` — a full code-review agent is out of scope
- `pr-metrics/pr-close-judge` — the verdict arrives later via Sentry's callback path
- `supergroups/cluster-lightweight` — requires a grouping model
- `assisted-query/create-cache` — internal prompt-caching optimization
- `monitoring-providers/gcp/verify-connection` — simulated when `GCP_SERVICE_ACCOUNT_JSON` is unset

**Now real (when configured):**

- `explorer/chat` — async run with LLM, code-index augmentation. The run carries a pending assistant block (`loading: true`) while the reply is generated and fills it in on completion, so polling clients always have something to render; a provider that does not answer within `OUTBOUND_TIMEOUT` fails the run with `failure_reason: "timeout"`
- `explorer/repos` — stored preferences or configured provider's repos
- `explorer/index/org-repo-knowledge` — fetches, chunks, embeds, stores repository code
- `agent/feature/run` — persisted run with LLM, idempotency key
- `autofix/coding-agent/state/set` — lazy run creation, background LLM advancement
- `summarize/trace`, `summarize/fixability` — LLM-backed with heuristic fallback
- `feedback/*` — all five endpoints call the LLM
- `replay/breadcrumbs/*` — persisted in SQLite, LLM-summarized
- `severity-score` — LLM with deterministic heuristic fallback
- `llm/generate` — LLM-backed
- `oneshot/run` — LLM-backed
- `codegen/unit-tests` — real when repository provider configured
- `investigations` — persisted runs with LLM workflow step
- `assisted-query/start/state/translate/translate-agentic` — LLM-backed with code-index context
- `offboarding/repository` — drops code index
- `pr-metrics/delegated-agent-match` — real autofix run lookup
- `project-preference/*` — persisted per-org repository preferences
- `monitoring-providers/gcp/verify-connection` — real when `GCP_SERVICE_ACCOUNT_JSON` configured

## Main gaps still remaining

### 1. Autofix uses a single LLM turn, not Seer's multi-turn tool-use loop

faux-seer advances the coding agent with a single LLM call per state-set. Seer's real agent loop involves iterative model/tool/model turns, tool invocation, and richer state transitions over time. The background advancement task observes cancellation and persists `"shutdown"` on process exit, but the execution model is simpler than Seer's.

### 2. Only GitHub is implemented as a repository provider

GitLab, Gitea, and Bitbucket are recognised by the factory but not implemented. When their tokens are set, `NewProvider` returns `ErrNotConfigured` and repository-backed endpoints fall back to simulated responses.

### 3. Several Seer-only behaviors remain simulated

The following endpoints are intentionally simulated because their dependencies are internal to Seer or require infrastructure not available externally:

- anomaly detection, breakpoint detection, cohort comparison (timeseries/statistical backends)
- code review (full code-review agent)
- issue detection (event-processing pipeline)
- supergroup clustering (grouping model)
- explorer index/sentry-knowledge, export-indexes, service-map/update (Seer-internal)

These endpoints return stable, structured responses that satisfy Sentry's parsing.

### 4. Runtime validation is incomplete in this environment

The checked-in Docker and backend code is in place, but this session environment could not perform full Docker runtime validation because Docker daemon access was denied.

That means the following still need verification in a Docker-capable environment:

- `docker build -t faux-seer:local .`
- `docker compose up --build`
- health checks against the running container
- validation that the Debian trixie image runs correctly with the CGO-linked `sqlite-vec` binary

### 5. End-to-end validation against real Sentry is still pending

The service was designed against local `seer` and `sentry` source contracts, but it still needs full live validation against an actual Sentry deployment.

Recommended checks:

- point Sentry's Seer URLs to `faux-seer`
- verify signed request compatibility across all implemented endpoints
- exercise autofix flows from the Sentry UI
- confirm grouping similarity behavior using real event payloads
- confirm summaries and severity are acceptable for the target workflow

### 6. Production-like backend validation is still limited

Both vector backends are implemented, but neither has been thoroughly exercised under production-like load in this environment.

Still recommended:

- test `sqlite-vec` with realistic embedding dimensions and record counts
- test `pgvector` against a live Postgres instance using real embeddings
- verify behavior for deletes, threshold tuning, and larger similarity datasets

### 7. Some schema and behavior tradeoffs remain intentionally pragmatic

The current implementation optimizes for compatibility and maintainability over perfect parity.

Examples:

- app state remains in SQLite even when `pgvector` is enabled
- the vector store contract still exposes supergroup insert support even though no route writes supergroups
- there are legacy SQLite grouping tables still present in the shared schema even though the active `sqlitevec` backend now uses its own `sqlite-vec`-aware table

These are not blockers, but they are useful to keep in mind if the next phase is cleanup or higher-fidelity parity.

## Recommended next steps

If you continue from here, the highest-value next tasks are:

1. Implement GitLab, Gitea, or Bitbucket as repository providers so non-GitHub orgs get real behavior.
2. Validate Docker runtime in an environment with Docker daemon access.
3. Connect a real Sentry instance and test end-to-end flows (especially explorer chat async lifecycle, autofix background advancement, and delegated-agent-match).
4. Increase autofix fidelity: multi-turn tool-use loop, richer state transitions, cancellation-aware shutdown.
5. Stress-test `sqlite-vec` and `pgvector` with realistic embedding dimensions, record counts, and code-index chunk volumes.

## Suggested handoff checklist

Before the next implementation phase, make sure you know:

- whether the target org uses GitHub or needs a different repository provider
- whether compatibility is sufficient or whether behavior parity is now the goal
- whether the next environment has Docker daemon access and a real Sentry instance available
- whether `GITHUB_TOKEN`, `GCP_SERVICE_ACCOUNT_JSON`, and a non-stub `LLM_PROVIDER` are configured for real behavior
