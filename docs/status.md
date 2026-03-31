# Current Status and Remaining Gaps

This document summarizes what `faux-seer` currently implements and what is still missing relative to the original compatibility-first build plan.

## What is implemented

The current codebase includes:

- Sentry-compatible HTTP auth using `Authorization: Rpcsignature rpc0:<hex>`
- health endpoints
- autofix start, update, state, prompt, and coding-agent state endpoints
- issue summary, trace summary, and fixability endpoints
- severity score endpoints
- grouping similarity, grouping record, and supergroup endpoints
- project preference endpoints
- repo access endpoint
- SQLite app-state persistence
- real `sqlite-vec` integration for the SQLite vector backend
- real `pgvector` integration for the advanced vector backend
- Sentry observability with `sentry-go` for errors, tracing, logs, and metrics
- Debian trixie Docker build/runtime images

The codebase also has passing:

- `go test ./...`
- `go vet ./...`
- `go build ./...`

## Main gaps still remaining

## 1. Autofix is not a real Seer-like agent loop

Autofix currently provides compatibility-oriented state persistence and prompt generation, but it does **not** implement Seer's deeper orchestration model.

Missing pieces include:

- a background execution loop
- tool invocation and iterative model/tool/model turns
- richer run state transitions over time
- cancellation-aware shutdown behavior that marks in-flight work as cancelled
- more realistic coding-agent execution behavior

Today, autofix is best understood as a compatibility stub that gives Sentry a pollable run record and a derived prompt, not a full autonomous repair workflow.

## 2. Behavior parity is still partial

The project aims for wire compatibility first, not full behavior parity with Python Seer.

Areas that remain heuristic or simplified:

- autofix reasoning and state progression
- severity scoring
- issue summaries
- trace summaries
- fixability output

These endpoints return stable, structured responses, but their logic is intentionally much simpler than real Seer.

## 3. Runtime validation is incomplete in this environment

The checked-in Docker and backend code is in place, but this session environment could not perform full Docker runtime validation because Docker daemon access was denied.

That means the following still need verification in a Docker-capable environment:

- `docker build -t faux-seer:local .`
- `docker compose up --build`
- health checks against the running container
- validation that the Debian trixie image runs correctly with the CGO-linked `sqlite-vec` binary

## 4. End-to-end validation against real Sentry is still pending

The service was designed against local `seer` and `sentry` source contracts, but it still needs full live validation against an actual Sentry deployment.

Recommended checks:

- point Sentry's Seer URLs to `faux-seer`
- verify signed request compatibility across all implemented endpoints
- exercise autofix flows from the Sentry UI
- confirm grouping similarity behavior using real event payloads
- confirm summaries and severity are acceptable for the target workflow

## 5. Production-like backend validation is still limited

Both vector backends are implemented, but neither has been thoroughly exercised under production-like load in this environment.

Still recommended:

- test `sqlite-vec` with realistic embedding dimensions and record counts
- test `pgvector` against a live Postgres instance using real embeddings
- verify behavior for deletes, threshold tuning, and larger similarity datasets

## 6. Some schema and behavior tradeoffs remain intentionally pragmatic

The current implementation optimizes for compatibility and maintainability over perfect parity.

Examples:

- app state remains in SQLite even when `pgvector` is enabled
- summaries and severity use simplified logic
- there are legacy SQLite grouping tables still present in the shared schema even though the active `sqlitevec` backend now uses its own `sqlite-vec`-aware table

These are not blockers, but they are useful to keep in mind if the next phase is cleanup or higher-fidelity parity.

## Recommended next steps

If you continue from here, the highest-value next tasks are:

1. Validate Docker runtime in an environment with Docker daemon access.
2. Connect a real Sentry instance and test end-to-end flows.
3. Improve autofix from compatibility-state persistence into a real background agent loop.
4. Increase behavior fidelity for summaries, severity, and autofix reasoning.
5. Stress-test `sqlite-vec` and `pgvector` with realistic data.

## Suggested handoff checklist

Before the next implementation phase, make sure you know:

- which gap matters most: runtime validation, Sentry integration, or autofix fidelity
- whether compatibility is sufficient or whether behavior parity is now the goal
- whether the next environment has Docker daemon access and a real Sentry instance available
