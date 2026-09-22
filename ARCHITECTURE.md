# Architecture

## System view

```text
                +----------------------+
                |        Sentry        |
                | signed RPC requests  |
                +----------+-----------+
                           |
                           v
                +----------------------+
                |      faux-seer       |
                |  net/http + ServeMux |
                +----+---------+-------+
                     |         | 
         +-----------+         +--------------------+
         v                                        v
+----------------------+              +------------------------+
|   LLM client layer   |              |  Embedding client      |
| stub/openai/         |              | stub/openai/           |
| openrouter/anthropic |              | openrouter/custom      |
+----------------------+              +-----------+------------+
                                                  |
                                                  v
                                  +-------------------------------+
                                  | Vector store abstraction      |
                                  | sqlitevec or pgvector         |
                                  +---------------+---------------+
                                                  |
                       +--------------------------+--------------------------+
                       v                                                     v
            +-----------------------+                            +----------------------+
            | SQLite app database   |                            | Postgres + pgvector |
            | autofix + explorer    |                            | grouping + supergrp |
            +-----------------------+                            +----------------------+
```

## Request lifecycle

### Health

Health routes are served directly from the HTTP layer and return `{"status":"ok"}`.

### Protected RPC routes

All protected endpoints pass through a shared auth wrapper in `internal/handler/server.go`:

1. read the raw request body
2. verify `Authorization: Rpcsignature rpc0:<hex>` against configured shared secrets
3. dispatch the raw body to the target handler
4. encode a JSON response or a structured `{"error":"..."}` payload

### Autofix lifecycle

Autofix runs themselves are orchestrated by Seer in a real deployment; faux-seer only
persists the coding-agent state snapshots Sentry reports for a run:

1. `POST /v1/automation/autofix/coding-agent/state/set` replaces the run's coding-agent map
2. `POST /v1/automation/autofix/coding-agent/state/update` merges an update into one agent entry

Both endpoints look the run up in SQLite and return `{"run_id": <id>, "status": "success"}`.
A run id or agent id that is unknown yields `status: "error"` with a message instead of an error
status code.

### Explorer lifecycle

1. `chat` starts or continues a run, calls the configured LLM client, and stores the block history
2. `state` and `state/pr` return the stored run state
3. `runs/by-ids` returns live run summaries keyed by run id so Sentry can batch-poll statuses
4. `update` mutates run state for interrupts, user input responses, and created PRs
5. `repos`, `index`, `index/*`, and `export-indexes` are acknowledgement stubs because faux-seer
   stores no search index or repository selection

`POST /v1/automation/agent/feature/run` is likewise an acknowledgement stub; it returns a synthetic
`run_id` because Sentry's outbox only requires a non-null run id in the response.

### Similarity lifecycle

1. the handler forwards the request to `internal/similarity`
2. the embedding client converts stacktraces to vectors
3. the selected vector store performs nearest-neighbor search or upsert
4. the response is encoded in a Seer-compatible shape

### Supergroup lifecycle

- `supergroups/get` and `supergroups/get-by-group-ids` list stored supergroup artifacts
- `supergroups/cluster-lightweight` is an acknowledgement stub; faux-seer performs no clustering

### Summary and severity lifecycle

- issue summaries can call the configured LLM client for short compatibility text
- fixability is a heuristic response
- severity is currently deterministic and heuristic, clamped to `[0,1]`

### Feedback and replay summary lifecycle

- feedback spam-detection, labels, title, label-groups, and summarize return deterministic or
  derived responses; no LLM call is made
- replay breadcrumbs start, state, and delete are frontend-facing; start creates a session and
  returns a completed status immediately, state is polled by the frontend, and delete is a
  status-only consumer

### Investigation lifecycle

- `investigations` (POST) creates a run and returns a synthetic `runId` with an empty projection
- `investigations/{run_id}/commands` accepts a workflow command and returns `accepted: true` with
  the echoed or generated `requestId` UUID
- `investigations/{run_id}` (GET) returns the current run state

All three are stubs; faux-seer stores no investigation state between calls.

### Issue detection lifecycle

- `issue-detection/analyze` accepts trace data and returns HTTP 202 with `{"success":true}`; the
  body is never parsed by Sentry
- `issue-detection/check-budget/{org_id}` always returns `{"has_budget":true}`; Sentry fails open
  when absent

### Assisted query lifecycle

- `assisted-query/start` returns a synthetic `run_id`
- `assisted-query/state` returns a completed session with empty steps
- `assisted-query/translate` and `translate-agentic` return empty responses with no unsupported
  reason
- `assisted-query/create-cache` is a status-only consumer

All are stubs; faux-seer performs no query translation or caching.

### Anomaly detection, breakpoints, and workflow lifecycle

- `anomaly-detection/detect` returns `{"success":true,"timeseries":[]}` (no anomalies)
- `anomaly-detection/alert-data` returns `{"success":true,"data":[]}` (no threshold data)
- `anomaly-detection/store` and `delete-alert-data` are status-only consumers
- `workflows/compare/cohort` returns `{"results":[]}`
- `trends/breakpoint-detector` returns `{"data":[]}` (no breakpoints)

None of these perform real detection; they return empty-result shapes that satisfy Sentry's
response parsing.

### Code review, offboarding, and PR metrics lifecycle

- `code_review/check/rerun`, `review-request`, and `pr-closed` are status-only consumers
- `offboarding/repository` is a status-only consumer
- `pr-metrics/delegated-agent-match` returns HTTP 202 ("no synchronous match")
- `pr-metrics/pr-close-judge` is a status-only consumer; the verdict arrives later via callback

### Models and monitoring provider lifecycle

- `GET /v1/models` reads the configured `LLM_MODEL` list from config and returns it; Sentry
  caches it for 10 minutes
- `POST /v1/llm/generate` is a stub that returns empty content and model; consumers parse content
  as JSON and handle failure
- `POST /v1/monitoring-providers/gcp/verify-connection` is simulated; it returns
  `"connected"` for every project without performing real GCP verification

### Project preference maintenance lifecycle

- `remove-repository`, `bulk-remove-repositories`, and `remove-handoffs-for-integration` are
  acknowledgement stubs; faux-seer stores no preferences and always returns `{"success":true}`

## Core abstractions

## LLM abstraction

`internal/llm/client.go` defines:

```go
type Client interface {
    Complete(context.Context, CompletionRequest) (string, error)
}
```

Implemented clients:

- `stub`
- OpenAI-compatible HTTP client
- Anthropic client

The Anthropic adapter translates faux-seer's internal completion request into Anthropic's messages API and converts the response back into plain text for the rest of the application.

`LLM_MODEL` is a comma-separated list. `config.ModelSelector` hands out model names round-robin, and every client asks for the next name per request, so a list of models spreads provider load.

### Adding a new LLM provider

1. implement `llm.Client`
2. keep request cancellation wired through `context.Context`
3. take a `[]string` of model names and rotate with `config.NewModelSelector`
4. add a case to `internal/llm/factory.go`
5. document required environment variables in `docs/providers.md`

## Embedding abstraction

`internal/embedding/client.go` defines:

```go
type Client interface {
    EmbedTexts(context.Context, []string) ([][]float32, error)
}
```

The current embedding implementations are:

- deterministic stub embeddings
- OpenAI-compatible embedding HTTP client

`EMBEDDING_MODEL` is also a comma-separated list and rotates through `config.ModelSelector`, the same as the LLM clients.

## Vector store abstraction

`internal/vectorstore/store.go` defines the contract used by similarity and supergroup handlers:

```go
type Store interface {
    UpsertGroupingRecords(context.Context, []GroupingRecord) error
    SearchSimilar(context.Context, int64, string, []float32, int, float64) ([]SimilarIssue, error)
    DeleteProject(context.Context, int64) (bool, error)
    DeleteHashes(context.Context, int64, []string) (bool, error)
    InsertSupergroup(context.Context, SupergroupRecord) error
    ListSupergroups(context.Context, int64, []int64, int, int) ([]map[string]any, error)
}
```

### `sqlitevec`

`internal/vectorstore/sqlitevec/store.go` uses the `sqlite-vec` CGO bindings with `mattn/go-sqlite3`. Vectors are serialized to the BLOB format expected by `sqlite-vec`, stored in SQLite, and queried with SQLite-side cosine-distance functions.

### `pgvector`

`internal/vectorstore/pgvector/store.go` uses Postgres plus the `vector` extension. It is the advanced backend for:

- grouping records
- nearest-neighbor similarity search
- supergroup artifacts

### Adding a new vector backend

1. implement `vectorstore.Store`
2. keep all I/O methods context-aware
3. add the backend to `internal/vectorstorefactory/factory.go`
4. extend configuration loading in `internal/config/config.go`
5. document operational requirements in `README.md` and `docs/providers.md`

## Persistence model

The service intentionally uses split persistence:

- SQLite application DB:
  - autofix runs
  - explorer runs
  - default local grouping records and supergroups
- `pgvector` Postgres DB:
  - grouping records
  - similarity search
  - supergroups when `VECTOR_STORE=pgvector`

This split keeps local setup simple while allowing better vector search when Postgres is available.

## HTTP and observability

`cmd/faux-seer/main.go` wires:

- config loading
- SQLite app store
- LLM and embedding providers
- vector store selection
- application services
- HTTP routing
- graceful shutdown
- Sentry SDK initialization and flush

Each request also passes through a structured access-log middleware that emits one record with:

- HTTP method
- full request URL (path plus query string)
- request headers
- request body
- final status code
- request duration in milliseconds

The middleware reads and restores the request body so the auth wrapper and handlers can still consume it. Startup, generic errors, and these access records are the only log lines the server emits.

When `SENTRY_DSN` is configured, faux-seer enables:

- request/error capture
- tracing middleware
- `slog` mirroring to Sentry Logs
- request count and duration metrics

## Testing strategy

The test suite uses the standard library plus in-memory doubles:

- auth verification tests
- HTTP handler tests for explorer, similarity, severity, and issue summary
- OpenAI-compatible client and embedding client tests via `httptest.NewServer`, including model round-robin assertions
- access-log middleware tests covering URL, headers, and body capture
- SQLite vector store round-trip tests

Support mocks live in `internal/testutil/`.
