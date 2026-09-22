# Agent Context

This file summarizes the current engineering context for `faux-seer` so future agents can continue work without re-discovering core decisions.

## Project goal

`faux-seer` is a Go-based compatibility layer for Sentry's Seer service. The implementation is guided by the local `seer` and `sentry` codebases and aims to preserve the Sentry-side wire contract rather than clone Seer's Python internals feature-for-feature.

## Current compatibility surface

Implemented HTTP routes are exactly the paths Sentry's Seer client can issue:

- health: `GET /health`, `GET /health/live`, `GET /health/ready`
- agent/explorer: `POST /v1/automation/agent/feature/run`, `POST /v1/automation/explorer/{chat,state,state/pr,update,repos}`,
  `POST /v1/automation/explorer/runs/by-ids`, `POST /v1/automation/explorer/{index,export-indexes}`,
  `POST /v1/automation/explorer/index/{org-repo-knowledge,org-project-knowledge,sentry-knowledge}`,
  `POST /v1/explorer/service-map/update`
- autofix/codegen: `POST /v1/automation/autofix/coding-agent/state/{set,update}`,
  `POST /v1/automation/codegen/unit-tests`, `POST /v1/automation/oneshot/run`
- summaries: `POST /v1/automation/summarize/{issue,trace,fixability}`,
  `POST /v1/automation/summarize/feedback/{spam-detection,labels,title,label-groups,summarize}`,
  `POST /v1/automation/summarize/replay/breadcrumbs/{start,state,delete}`
- investigations: `POST /v1/automation/investigations`, `POST /v1/automation/investigations/{run_id}/commands`,
  `GET /v1/automation/investigations/{run_id}`
- issue detection: `POST /v1/automation/issue-detection/analyze`, `GET /v1/automation/issue-detection/check-budget/{org_id}`
- assisted query: `POST /v1/assisted-query/{state,start,translate,translate-agentic,create-cache}`
- anomaly/workflows/breakpoints: `POST /v1/anomaly-detection/{detect,alert-data,store,delete-alert-data}`,
  `POST /v1/workflows/compare/cohort`, `POST /trends/breakpoint-detector`
- code review/PR metrics: `POST /v1/code_review/{check/rerun,review-request,pr-closed}`, `POST /v1/offboarding/repository`,
  `POST /v1/pr-metrics/{delegated-agent-match,pr-close-judge}`
- grouping/supergroups/severity/models: `POST /v0/issues/similar-issues`,
  `GET /v0/issues/similar-issues/grouping-record/delete/{project_id}`,
  `POST /v0/issues/similar-issues/grouping-record/delete-by-hash`,
  `POST /v0/issues/supergroups/{cluster-lightweight,get,get-by-group-ids}`, `POST /v0/issues/severity-score`,
  `GET /v1/models`, `POST /v1/llm/generate`, `POST /v1/monitoring-providers/gcp/verify-connection`
- project preference maintenance: `POST /v1/project-preference/{remove-repository,bulk-remove-repositories,remove-handoffs-for-integration}`

Routes are limited to Seer endpoints reachable from Sentry's Seer client layer (`src/sentry/seer/**` plus
`feedback/lib/seer_api.py`, `replays/lib/seer_api.py`, `investigations/seer_client.py`, `pr_metrics/judge.py`,
`tasks/llm_issue_detection/detection.py`, `integrations/gcp/client.py`, `event_manager.py`, `deletions/tasks/seer.py`).
Paths with no call site anywhere in the Sentry tree are not served; `internal/handler/routes_test.go` pins both
halves of that contract (present paths answer, absent paths 404).

## Auth contract

Sentry compatibility uses the request signing format discovered from local Sentry sources:

- Header: `Authorization`
- Format: `Rpcsignature rpc0:<hex>`
- Algorithm: HMAC-SHA256 over the raw request body
- Config: `SEER_SHARED_SECRET` in faux-seer

If no shared secret is configured, auth verification is skipped for local development.

## Storage architecture

The project intentionally uses split storage:

- SQLite app DB:
  - autofix run state
  - explorer run state
- Vector store backend:
  - grouping similarity vectors
  - supergroup artifacts

Two vector backends exist:

- `sqlitevec`
  - local default
  - SQLite persistence plus Go-side cosine search
- `pgvector`
  - advanced backend
  - Postgres + `vector` extension
  - configured with `VECTOR_STORE=pgvector`

Important: even when `pgvector` is enabled, autofix and explorer state remain on SQLite by design.

## Model configuration

`LLM_MODEL` and `EMBEDDING_MODEL` are comma-separated lists. `config.ModelSelector` rotates them round-robin per provider request, so multiple models spread provider load. The similarity response's `model_used` reports the first configured embedding model.

## Observability

The service now includes `sentry-go` for its own monitoring.

Environment variables:

- `SENTRY_DSN`
- `SENTRY_SAMPLE_RATE`
- `SENTRY_TRACES_SAMPLE_RATE`
- `SENTRY_SEND_DEFAULT_PII`

Enabled features:

- error monitoring
- tracing
- logs via `sentry-go/slog`
- metrics via `sentry.NewMeter`

Implementation notes:

- initialization lives in `internal/observability/sentry.go`
- HTTP middleware is applied at server startup
- `slog` output is mirrored to stdout and Sentry Logs
- request count and duration metrics are emitted with request context
- the only log lines the server emits are a single startup line, generic errors, and one
  per-request access record containing method, full URL, headers, body, status, and duration

## Deployment files

Added deployment artifacts:

- `Dockerfile`
- `docker-compose.yml`

`docker-compose.yml` includes:

- `faux-seer`
- `pgvector/pgvector:pg16`

## Key docs

- `README.md` for setup and behavior
- `ARCHITECTURE.md` for system design
- `docs/endpoints.md` for route examples
- `docs/providers.md` for provider and pgvector setup

## Validation status

Most recently validated successfully with:

- `go test ./...`
- `go vet ./...`
- `go build ./...`

`docker compose config` was also validated.

A full `docker build` could not be run in the current environment earlier because of Docker socket permissions, but the checked-in Docker files are present and the compose configuration renders correctly.

## Known limitations

- Behavior is compatibility-focused and heuristic in several places.
- Autofix only persists coding-agent state; it does not reproduce Seer's full Python agent loop.
- Explorer indexing, repo, cluster-lightweight, and agent feature-run endpoints are acknowledgement stubs.
- `pgvector` only covers vector-backed surfaces, not all persistence.
- The local toolchain rewrote `go.mod` to `go 1.26.1`; that is currently the validated module state in this environment.

## Best next steps

If more work is requested, likely next areas are:

- richer autofix coding-agent state transitions
- stronger pgvector integration tests against a real Postgres service
- Docker build/runtime validation in an environment with daemon access
- more complete issue-summary / severity behavior against real providers
- real explorer indexing and repo tracking behind the acknowledgement stubs
