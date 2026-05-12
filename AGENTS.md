# Agent Context

This file summarizes the current engineering context for `faux-seer` so future agents can continue work without re-discovering core decisions.

## Project goal

`faux-seer` is a Go-based compatibility layer for Sentry's Seer service. The implementation is guided by the local `seer` and `sentry` codebases and aims to preserve the Sentry-side wire contract rather than clone Seer's Python internals feature-for-feature.

## Current compatibility surface

Implemented HTTP routes include:

- `GET /health`
- `GET /health/live`
- `GET /health/ready`
- `POST /v1/automation/autofix/start`
- `POST /v1/automation/autofix/update`
- `POST /v1/automation/autofix/state`
- `POST /v1/automation/autofix/state/pr`
- `POST /v1/automation/autofix/prompt`
- `POST /v1/automation/autofix/coding-agent/state/set`
- `POST /v1/automation/autofix/coding-agent/state/update`
- `POST /v1/automation/codebase/repo/check-access`
- `POST /v1/automation/summarize/issue`
- `POST /v1/automation/summarize/trace`
- `POST /v1/automation/summarize/fixability`
- `POST /v1/project-preference`
- `POST /v1/project-preference/set`
- `POST /v1/project-preference/bulk`
- `POST /v1/project-preference/bulk-set`
- `POST /v1/project-preference/remove-repository`
- `POST /v0/issues/similar-issues`
- `POST /v0/issues/similar-issues/grouping-record`
- `GET /v0/issues/similar-issues/grouping-record/delete/{project_id}`
- `POST /v0/issues/similar-issues/grouping-record/delete-by-hash`
- `POST /v0/issues/supergroups`
- `POST /v0/issues/supergroups/list`
- `POST /v0/issues/supergroups/get`
- `POST /v0/issues/supergroups/get-by-group-ids`
- `POST /v0/issues/severity-score`
- `POST /v1/issues/severity-score`

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
  - project preferences
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

Important: even when `pgvector` is enabled, autofix state and project preferences remain on SQLite by design.

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
- Autofix does not reproduce Seer's full Python agent loop.
- `pgvector` only covers vector-backed surfaces, not all persistence.
- The local toolchain rewrote `go.mod` to `go 1.26.1`; that is currently the validated module state in this environment.

## Best next steps

If more work is requested, likely next areas are:

- richer autofix state transitions and prompt fidelity
- stronger pgvector integration tests against a real Postgres service
- Docker build/runtime validation in an environment with daemon access
- more complete issue-summary / severity behavior against real providers
