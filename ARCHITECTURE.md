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
            | autofix + preferences |                            | grouping + supergrp |
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

Current autofix behavior is compatibility-mode persistence, not a background agent loop:

1. `start` stores an autofix run in SQLite
2. faux-seer generates a completed placeholder state with steps, codebase metadata, and the original request
3. `update` appends progress entries and may associate provider / PR metadata
4. `state` and `state/pr` retrieve the stored JSON blob
5. `prompt` derives a coding-agent prompt from persisted state

This gives Sentry a pollable run record without reproducing Seer's full Python orchestration model.

### Similarity lifecycle

1. the handler forwards the request to `internal/similarity`
2. the embedding client converts stacktraces to vectors
3. the selected vector store performs nearest-neighbor search or upsert
4. the response is encoded in a Seer-compatible shape

### Summary and severity lifecycle

- issue summaries can call the configured LLM client for short compatibility text
- trace summaries and fixability are heuristic responses
- severity is currently deterministic and heuristic, clamped to `[0,1]`

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

### Adding a new LLM provider

1. implement `llm.Client`
2. keep request cancellation wired through `context.Context`
3. add a case to `internal/llm/factory.go`
4. document required environment variables in `docs/providers.md`

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
  - project preferences
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

When `SENTRY_DSN` is configured, faux-seer enables:

- request/error capture
- tracing middleware
- `slog` mirroring to Sentry Logs
- request count and duration metrics

## Testing strategy

The test suite uses the standard library plus in-memory doubles:

- auth verification tests
- HTTP handler tests for autofix, similarity, severity, and issue summary
- OpenAI-compatible client tests via `httptest.NewServer`
- SQLite vector store round-trip tests

Support mocks live in `internal/testutil/`.
