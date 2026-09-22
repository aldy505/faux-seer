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

Autofix persists coding-agent state snapshots and advances the agent via a background LLM call:

1. `POST /v1/automation/autofix/coding-agent/state/set` replaces the run's coding-agent map. When the run does not yet exist, the service creates it on demand with the caller-supplied id (`CreateAutofixRunWithID`), because Sentry reports state for runs Seer already created.
2. `POST /v1/automation/autofix/coding-agent/state/update` merges an update into one agent entry, locating the run by scanning all persisted runs for the agent id.
3. After persisting state, the service kicks off a background `agentAdvancementTask` via the run manager. The task reads the stored coding-agent state, calls the LLM once to advance the agent, and persists the updated state.
4. On process shutdown the run manager cancels the root context; the advancement task detects cancellation, persists `status: "error"` with `failure_reason: "shutdown"`, and returns.

A run id that is unknown when state arrives is lazily created; an agent id that is unknown returns `status: "error"` with a message.

### Explorer lifecycle

1. `chat` validates the request, creates an `ExplorerRunRecord` holding the user block with `status: "processing"`, persists it, and dispatches a background `chatTask` via the run manager. The task builds a prompt from `query`, `page_name`, and `on_page_context`. When the code index has entries for the organization and the query looks code-related (file extensions, function names, stack frames), the task calls `codeindex.Search` and appends the top chunks to the prompt. It then calls the LLM, appends user and assistant blocks, and sets `status: "completed"` (or `status: "error"` with `failure_reason: "shutdown"` on cancellation). The HTTP response returns immediately with `run_id`, `has_explorer_index`, and `has_org_project_context`.
2. `state` and `state/pr` return the stored run state including `status`, `blocks[]`, and `failure_reason` when present. The run status is one of Sentry's literals: `"processing"`, `"completed"`, `"error"`, or `"awaiting_user_input"`.
3. `runs/by-ids` returns live run summaries keyed by run id so Sentry can batch-poll statuses
4. `update` mutates run state for interrupts, user input responses, and created PRs
5. `repos` returns stored repository preferences for the organization when they exist, or the configured repository provider's repositories via `Provider.ListRepos`. Without a configured provider, the empty list is the simulated answer.
6. `index/org-repo-knowledge` fetches, chunks, embeds, and stores repository code via `codeindex.IndexRepo` in the background. It skips repositories the org has removed via project preferences.
7. `index` (generic), `index/org-project-knowledge`, `index/sentry-knowledge`, `export-indexes`, and `service-map/update` are simulated because their requests carry no repository identity or target Seer-internal infrastructure.

`POST /v1/automation/agent/feature/run` creates a persisted run via `runs.Service.Start`, calls the LLM for a short result, and returns the run id. Retried requests with the same `ExternalIdempotencyKey` reuse the existing run.

### Similarity lifecycle

1. the handler forwards the request to `internal/similarity`
2. the embedding client converts stacktraces to vectors
3. the selected vector store performs nearest-neighbor search or upsert
4. the response is encoded in a Seer-compatible shape

### Supergroup lifecycle

- `supergroups/get` and `supergroups/get-by-group-ids` list stored supergroup artifacts
- `supergroups/cluster-lightweight` is an acknowledgement stub; faux-seer performs no clustering

### Summary and severity lifecycle

- `summarize/issue` calls the LLM and returns structured headline, whats_wrong, trace, possible_cause, and scores
- `summarize/trace` calls the LLM with trace context and returns summary, key_observations, performance_characteristics, and suggested_investigations; falls back to a deterministic heuristic on failure
- `summarize/fixability` calls the LLM and returns a fixability assessment with the same shape as issue summaries
- `severity-score` calls the LLM for a severity JSON `{"severity": 0.0..1.0}` and falls back to the deterministic token/heuristic scoring when the LLM fails, so the endpoint never 500s

### Feedback and replay summary lifecycle

- `feedback/spam-detection` calls the LLM with a boolean classification prompt and returns `{"is_spam": bool}`
- `feedback/labels` calls the LLM to produce a JSON array of category labels
- `feedback/title` calls the LLM to produce a title; falls back to the deterministic `DeriveFeedbackTitle` heuristic when the LLM returns empty
- `feedback/label-groups` calls the LLM to group labels with associated labels; returns one entry per requested label
- `feedback/summarize` calls the LLM to summarize a list of feedback messages
- All feedback handlers return HTTP 400 for both malformed requests and LLM failures
- `replay/breadcrumbs/start` validates the request, upserts a `replay_breadcrumb_summaries` row with `status: "processing"`, calls the LLM with a synthesized breadcrumb narrative, stores the summary, and returns it with `status: "completed"`
- `replay/breadcrumbs/state` reads the stored row and returns the same shape; when no summary exists for the replay_id, returns `status: "not_started"`
- `replay/breadcrumbs/delete` removes stored rows and returns `{"success": true}`

### Investigation lifecycle

- `investigations` (POST) creates a persisted run via `runs.Service.Start` with an idempotency key derived from `requestId`. The background task calls the LLM with an investigation prompt and merges the answer into the run projection. Returns `{"runId": <id>, "created": true, "projection": {}}`.
- `investigations/{run_id}/commands` validates `requestId` and `expectedWorkflowVersion`, records the command via `AcceptCommand` (deduplicating by request id), increments the workflow version, and returns the accepted command with the updated version. A repeated `requestId` is reported as `duplicate: true`.
- `investigations/{run_id}` (GET) reads the persisted run and returns the projection, which contains the run summary plus the most recent accepted command.

All three are persisted in the `runs` and `run_commands` SQLite tables.

### Issue detection lifecycle

- `issue-detection/analyze` accepts trace data and returns HTTP 202 with `{"success":true}`; the
  body is never parsed by Sentry
- `issue-detection/check-budget/{org_id}` always returns `{"has_budget":true}`; Sentry fails open
  when absent

### Assisted query lifecycle

- `assisted-query/start` creates a persisted run via `runs.Service.Start` and dispatches a background `answerTask` that calls the LLM, grounded in code-index context when the organization has an index. Returns `{"run_id": <id>}`.
- `assisted-query/state` reads the persisted run and returns the session with `status`, `current_step`, `completed_steps`, and `final_response`.
- `assisted-query/translate` calls the LLM with the natural-language question and parses the model's JSON response into a list of `Query` objects (each with `query`, `stats_period`, `sort`, `group_by`, `visualization`, and `mode`). Code-index context is included when available.
- `assisted-query/translate-agentic` uses the same translation logic as `translate`.
- `assisted-query/create-cache` is a simulated ack; prompt caching is an internal Seer optimization.

### Anomaly detection, breakpoints, and workflow lifecycle

- `anomaly-detection/detect` returns `{"success":true,"timeseries":[]}` (no anomalies)
- `anomaly-detection/alert-data` returns `{"success":true,"data":[]}` (no threshold data)
- `anomaly-detection/store` and `delete-alert-data` are status-only consumers
- `workflows/compare/cohort` returns `{"results":[]}`
- `trends/breakpoint-detector` returns `{"data":[]}` (no breakpoints)

None of these perform real detection; they return empty-result shapes that satisfy Sentry's
response parsing.

### Code review, offboarding, and PR metrics lifecycle

- `code_review/check/rerun`, `review-request`, and `pr-closed` are simulated acks; a full code-review agent is out of scope
- `offboarding/repository` parses the request for `provider`, `repository_owner`, and `repository_name`, then calls `codeindex.DeleteRepo` to drop stale chunks. Always returns `{"success": true}`.
- `pr-metrics/delegated-agent-match` looks up the autofix run by provider+PR pair or by PR URL substring in the state blob. On a match it returns HTTP 200 with `run_id`, `agent_id`, `signal_type`, and `match_path`. On no match it returns HTTP 202.
- `pr-metrics/pr-close-judge` is a simulated ack; the actual verdict arrives later via Sentry's callback path

### Models and monitoring provider lifecycle

- `GET /v1/models` reads the configured `LLM_MODEL` list from config and returns it; Sentry caches it for 10 minutes
- `POST /v1/llm/generate` calls the LLM with the request's `system_prompt`, `prompt`, `temperature`, and `max_tokens` and returns `{"content": text, "model": model}`. Consumers parse content as JSON and handle failure.
- `POST /v1/automation/oneshot/run` calls the LLM and returns `{"result": {...}}`. The result keys vary by `oneshot_id`: `"conversation_title"` returns `{"title": ...}`, `"agent_question"` returns `{"answer": ...}`.
- `POST /v1/monitoring-providers/gcp/verify-connection` performs real GCP project verification when `GCP_SERVICE_ACCOUNT_JSON` is configured: it loads the service account, exchanges a signed JWT for an OAuth2 access token, and checks project access via Cloud Resource Manager and Service Usage APIs. When the env var is unset, it returns `"connected"` for every project without performing real verification.

### Project preference maintenance lifecycle

- All three endpoints persist per-organization repository preferences in the `project_preferences` SQLite table.
- `remove-repository` removes the matching `{provider, external_id}` from the stored `repos_json` list and saves.
- `bulk-remove-repositories` removes all entries matching the request's `repositories` list and saves.
- `remove-handoffs-for-integration` clears the `integration_id` field and saves.
- The `preferences.RemovedSet` method is consulted by explorer indexing so that a repository removed from Seer is not indexed again.

## Core abstractions

### Git provider interface

`internal/git/provider.go` defines:

```go
type Provider interface {
    Name() string
    ListRepos(ctx context.Context, org string) ([]Repo, error)
    GetRepo(ctx context.Context, owner, name string) (Repo, error)
    GetDefaultBranch(ctx context.Context, owner, name string) (string, error)
    ReadTree(ctx context.Context, owner, name, ref string) ([]TreeEntry, error)
    ReadFile(ctx context.Context, owner, name, ref, path string) ([]byte, error)
    GetPR(ctx context.Context, owner, name string, number int) (PullRequest, error)
    GetPRFiles(ctx context.Context, owner, name string, number int) ([]PRFile, error)
    CreateBranch(ctx context.Context, owner, name, baseBranch, newBranch string) error
    CreateCommit(ctx context.Context, owner, name, branch, message string, files []FileChange) (string, error)
    OpenPR(ctx context.Context, owner, name, title, body, head, base string) (PullRequest, error)
    PostPRReview(ctx context.Context, owner, name string, number int, body string, comments []PRReviewComment) error
}
```

`internal/git/factory.go` `NewProvider` maps `"github"`, `"gitlab"`, `"gitea"`, `"bitbucket"` to their constructors. Only GitHub (`internal/git/github.go`) is implemented; the others return `ErrNotConfigured` so handlers fall back to simulated responses. The GitHub implementation uses stdlib `net/http` against `GITHUB_BASE_URL` with `GITHUB_TOKEN` as `Authorization: Bearer`.

### Code index service

`internal/codeindex/service.go` provides `IndexRepo`, `Search`, `DeleteRepo`, and `HasOrgIndex`. Indexing fetches the default branch when `ref` is empty, walks the tree via `git.Provider.ReadTree`, skips binary files (NUL byte detection) and files larger than 1 MiB, chunks text into `CODE_INDEX_CHUNK_SIZE` runes with `CODE_INDEX_CHUNK_OVERLAP` overlap, embeds chunks via `embedding.Client.EmbedTexts`, and stores them in the vector backend. `Search` embeds the query and returns top-K code chunks. The service is nil-safe: when no git provider is configured, `Indexed()` returns false and handlers fall back to simulated responses.

### Run manager

`internal/runmgr/runmgr.go` runs background work with process-lifetime cancellation. The `Manager` owns a `sync.Map` of in-flight `Task` goroutines and a root `context.Context` derived from the process signal context. `Start(key, task)` stores the task and spawns it in a goroutine. `Cancel(key)` stops one run. `CancelAll()` cancels the root context, which propagates to every in-flight run. `WaitContext(ctx)` blocks until all runs finish or the context expires. On `SIGINT`/`SIGTERM`, `main.go` calls `CancelAll()` before `httpServer.Shutdown`, so in-flight runs persist their terminal `"error"` / `"shutdown"` status before the process exits.

### Persisted runs

`internal/runs/service.go` wraps the `runs` and `run_commands` SQLite tables. `Start` persists a run and dispatches a background `Task` via the run manager. `StartWithID` does the same but uses a caller-supplied id (for autofix runs where Sentry already holds the id). `FindByIdempotencyKey` deduplicates retried requests. `AcceptCommand` records a command once per `request_id` and advances the run's `workflow_version`; a repeated `request_id` is reported as `duplicate: true`. `Complete`, `Fail`, and `UpdateProjection` persist terminal or intermediate states. `Projection` renders the stored result as the map Sentry reads from the `GET` endpoint. `CompleteText` runs a single LLM turn and returns trimmed text, used by the agent feature run and investigation background tasks.

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
  - autofix runs (with `status` column and `provider`/`pr_id` for delegated-agent-match lookup)
  - explorer runs (with `status` column)
  - generic runs (`runs` table with `kind`, `idempotency_key`, `status`, `workflow_version`)
  - run commands (`run_commands` table with deduplicated `request_id`)
  - replay breadcrumb summaries (`replay_breadcrumb_summaries` table)
  - project preferences (`project_preferences` table with `repos_json` and `integration_id`)
  - default local grouping records and supergroups
- Vector store backend (sqlitevec or pgvector):
  - grouping records and similarity search
  - supergroup artifacts
  - code-indexed repository chunks (`sqlitevec_code_chunks` or `code_chunks`)

The `status` column on `autofix_runs` and `explorer_runs` defaults to `"completed"` for older databases that predate the column. New runs use `"processing"` while background work is in flight.

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
- HTTP handler tests for explorer (including async run lifecycle), similarity, severity, issue summary, explorer indexing, preferences, and generation
- `internal/handler/routes_test.go` pins the exact route surface: 63 Seer paths answer, absent paths 404, and `issue-detection/analyze` returns exactly 202
- OpenAI-compatible client and embedding client tests via `httptest.NewServer`, including model round-robin assertions
- access-log middleware tests covering URL, headers, and body capture
- SQLite vector store round-trip tests (grouping, code chunks, has-org-index)
- `internal/git/` tests for the GitHub provider via `httptest.NewServer`
- `internal/codeindex/` tests for chunking, binary detection, and index/delete
- `internal/explorer/` tests for chat run lifecycle, code-context detection, and state persistence

Support mocks live in `internal/testutil/`.
