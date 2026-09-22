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

- SQLite app-state persistence (autofix runs, explorer runs, grouping records, supergroups)
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

## What is a stub or simulated rather than real

The following endpoints are acknowledgement stubs, simulated responses, or deterministic heuristics. They return valid, stable JSON but do not perform real work:

- **Acknowledgement stubs** (accept any JSON, return `{"success":true}`): `explorer/index`, `explorer/index/org-repo-knowledge`, `explorer/index/org-project-knowledge`, `explorer/index/sentry-knowledge`, `explorer/export-indexes`, `explorer/repos`, `explorer/service-map/update`, `agent/feature/run`, `codegen/unit-tests`, `assisted-query/create-cache`, `anomaly-detection/store`, `anomaly-detection/delete-alert-data`, `code_review/check/rerun`, `code_review/review-request`, `code_review/pr-closed`, `offboarding/repository`, `supergroups/cluster-lightweight`, `project-preference/remove-repository`, `project-preference/bulk-remove-repositories`, `project-preference/remove-handoffs-for-integration`
- **Simulated responses** (structured but not real): `anomaly-detection/detect` (empty timeseries), `anomaly-detection/alert-data` (empty data), `workflows/compare/cohort` (empty results), `trends/breakpoint-detector` (empty data), `monitoring-providers/gcp/verify-connection` (always "connected"), `llm/generate` (empty content/model)
- **Deterministic heuristics** (simple logic, not LLM-backed): `issues/severity-score`, `summarize/trace`, `summarize/fixability`
- **Derived/stub summaries**: `summarize/feedback/spam-detection`, `summarize/feedback/labels`, `summarize/feedback/title`, `summarize/feedback/label-groups`, `summarize/feedback/summarize`, `summarize/replay/breadcrumbs/start`, `summarize/replay/breadcrumbs/state`, `summarize/replay/breadcrumbs/delete`
- **Stub state stores**: `investigations` (POST), `investigations/{run_id}/commands`, `investigations/{run_id}` (GET), `assisted-query/start`, `assisted-query/state`, `assisted-query/translate`, `assisted-query/translate-agentic`, `oneshot/run`
- **202 ack stubs** (accepted but not processed): `issue-detection/analyze`, `pr-metrics/delegated-agent-match`
- **Lightweight ack**: `pr-metrics/pr-close-judge`, `issue-detection/check-budget/{org_id}`

## Main gaps still remaining

### 1. Autofix is not a real Seer-like agent loop

faux-seer persists the coding-agent state snapshots Sentry reports, but it does **not** implement Seer's orchestration model. Runs themselves are created and advanced by Seer in a real deployment.

Missing pieces include:

- run creation and a background execution loop
- tool invocation and iterative model/tool/model turns
- richer run state transitions over time
- cancellation-aware shutdown behavior that marks in-flight work as cancelled
- more realistic coding-agent execution behavior

Today, autofix is best understood as a compatibility stub that stores coding-agent state for runs Sentry tells it about.

### 2. Behavior parity is still partial

The project aims for wire compatibility first, not full behavior parity with Python Seer.

Areas that remain heuristic, simplified, or acknowledgement-only:

- autofix state progression
- explorer indexing, repo listing, lightweight clustering, and agent feature runs
- severity scoring
- issue summaries
- fixability output
- assisted query, anomaly detection, breakpoints, workflows
- code review, offboarding, PR metrics
- project-preference maintenance
- GCP verification, LLM/generate

These endpoints return stable, structured responses, but their logic is intentionally much simpler than real Seer.

### 3. Runtime validation is incomplete in this environment

The checked-in Docker and backend code is in place, but this session environment could not perform full Docker runtime validation because Docker daemon access was denied.

That means the following still need verification in a Docker-capable environment:

- `docker build -t faux-seer:local .`
- `docker compose up --build`
- health checks against the running container
- validation that the Debian trixie image runs correctly with the CGO-linked `sqlite-vec` binary

### 4. End-to-end validation against real Sentry is still pending

The service was designed against local `seer` and `sentry` source contracts, but it still needs full live validation against an actual Sentry deployment.

Recommended checks:

- point Sentry's Seer URLs to `faux-seer`
- verify signed request compatibility across all implemented endpoints
- exercise autofix flows from the Sentry UI
- confirm grouping similarity behavior using real event payloads
- confirm summaries and severity are acceptable for the target workflow

### 5. Production-like backend validation is still limited

Both vector backends are implemented, but neither has been thoroughly exercised under production-like load in this environment.

Still recommended:

- test `sqlite-vec` with realistic embedding dimensions and record counts
- test `pgvector` against a live Postgres instance using real embeddings
- verify behavior for deletes, threshold tuning, and larger similarity datasets

### 6. Some schema and behavior tradeoffs remain intentionally pragmatic

The current implementation optimizes for compatibility and maintainability over perfect parity.

Examples:

- app state remains in SQLite even when `pgvector` is enabled
- summaries and severity use simplified logic
- there are legacy SQLite grouping tables still present in the shared schema even though the active `sqlitevec` backend now uses its own `sqlite-vec`-aware table
- the vector store contract still exposes supergroup insert support even though no route writes supergroups
- acknowledgement stubs return synthetic run ids because faux-seer keeps no feature-run records

These are not blockers, but they are useful to keep in mind if the next phase is cleanup or higher-fidelity parity.

## Recommended next steps

If you continue from here, the highest-value next tasks are:

1. Validate Docker runtime in an environment with Docker daemon access.
2. Connect a real Sentry instance and test end-to-end flows.
3. Replace the explorer index/repo/agent feature-run stubs with real behavior.
4. Increase behavior fidelity for summaries, severity, and autofix state.
5. Stress-test `sqlite-vec` and `pgvector` with realistic data.

## Suggested handoff checklist

Before the next implementation phase, make sure you know:

- which gap matters most: runtime validation, Sentry integration, or autofix fidelity
- whether compatibility is sufficient or whether behavior parity is now the goal
- whether the next environment has Docker daemon access and a real Sentry instance available
