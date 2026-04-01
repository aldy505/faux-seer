# faux-seer

`faux-seer` is a self-hosted, Go-based compatibility layer for Sentry's Seer service. It implements the signed HTTP endpoints Sentry expects for autofix, summaries, similarity, severity, and project preferences, while delegating text generation and embeddings to configurable providers or safe local stubs.

The goal is wire compatibility with Sentry's Seer integration, not a full reimplementation of Seer's Python internals.

## Quick start

```bash
cp .env.example .env
# edit provider credentials and shared secret
docker compose up --build
```

For local development without Docker:

```bash
cp .env.example .env
go run ./cmd/faux-seer
```

The service listens on `:9091` by default.

## Pointing Sentry at faux-seer

Point Sentry's Seer URLs at your faux-seer instance:

- `SEER_DEFAULT_URL`
- `SEER_AUTOFIX_URL`
- `SEER_SUMMARIZATION_URL`
- `SEER_GROUPING_URL`

If you are running `faux-seer` directly on your host machine on the default port, set all four to the same base URL:

```bash
SEER_DEFAULT_URL=http://127.0.0.1:9091
SEER_AUTOFIX_URL=http://127.0.0.1:9091
SEER_SUMMARIZATION_URL=http://127.0.0.1:9091
SEER_GROUPING_URL=http://127.0.0.1:9091
```

If your Sentry containers need to reach a `faux-seer` container over a Docker network, use the service hostname instead. For example, if the service name is `faux-seer` and it listens on port `9091`:

```bash
SEER_DEFAULT_URL=http://faux-seer:9091
SEER_AUTOFIX_URL=http://faux-seer:9091
SEER_SUMMARIZATION_URL=http://faux-seer:9091
SEER_GROUPING_URL=http://faux-seer:9091
```

In the current implementation, using the same base URL for all four settings is correct because `faux-seer` serves all of those compatibility endpoints from one HTTP server.

The shared secret must match on both sides:

- Sentry side: `SEER_RPC_SHARED_SECRET` or `SEER_API_SHARED_SECRET`, depending on your local Sentry setup
- faux-seer side: `SEER_SHARED_SECRET`

Sentry signs requests with:

```text
Authorization: Rpcsignature rpc0:<hex>
```

The signature is HMAC-SHA256 over the raw request body.

## Implemented compatibility surface

Implemented routes include:

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
- grouping record and supergroup endpoints under `/v0/issues/...`
- `POST /v0/issues/severity-score`
- `POST /v1/issues/severity-score`

See `docs/endpoints.md` for request and response examples.

See `docs/status.md` for the current implementation status, known gaps, and recommended next steps.

## Configuration

The repo targets Go `1.26.x`.

Core environment variables:

| Variable | Purpose |
| --- | --- |
| `ADDR` | HTTP bind address, default `:9091` |
| `DATABASE_PATH` | SQLite database path, default `data/faux-seer.db` |
| `SEER_SHARED_SECRET` | Shared HMAC secret for Sentry-compatible RPC signing |
| `LOG_LEVEL` | Logging level |
| `VECTOR_STORE` | `sqlitevec` or `pgvector` |
| `VECTOR_STORE_DSN` | Required when `VECTOR_STORE=pgvector` |
| `VECTOR_DIMENSIONS` | Embedding/vector width for pgvector storage |
| `SIMILARITY_THRESHOLD` | Default nearest-neighbor threshold |

## LLM and embedding providers

`LLM_PROVIDER` accepts:

- `stub`
- `openai`
- `openrouter`
- `anthropic`
- `custom`

`EMBEDDING_PROVIDER` accepts:

- `stub`
- `openai`
- `openrouter`
- `custom`

`stub` is useful for local compatibility testing because it requires no external credentials and keeps responses deterministic.

Example OpenAI-compatible setup:

```bash
LLM_PROVIDER=openai
LLM_API_KEY=sk-...
LLM_MODEL=gpt-4.1-mini

EMBEDDING_PROVIDER=openai
EMBEDDING_API_KEY=sk-...
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIMENSIONS=1536
```

Example OpenRouter setup:

```bash
LLM_PROVIDER=openrouter
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_API_KEY=sk-or-...
LLM_MODEL=google/gemini-2.5-flash
HTTP_REFERER=https://your-local-dev-host.example

EMBEDDING_PROVIDER=openrouter
EMBEDDING_BASE_URL=https://openrouter.ai/api/v1
EMBEDDING_API_KEY=sk-or-...
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIMENSIONS=1536
```

Example Anthropic setup:

```bash
LLM_PROVIDER=anthropic
LLM_API_KEY=sk-ant-...
LLM_MODEL=claude-3-7-sonnet-latest
```

See `docs/providers.md` for more complete provider notes.

## Vector store backends

### `VECTOR_STORE=sqlitevec`

This is the default local mode. It now uses the real `sqlite-vec` extension through the CGO Go bindings.

- SQLite persistence for grouping records
- `sqlite-vec`-backed vector serialization and DB-side cosine-distance queries
- no external Postgres dependency
- requires a CGO-capable build environment

In this repo, the Go binary statically links `sqlite-vec` via `github.com/asg017/sqlite-vec-go-bindings/cgo`, so you do not need to install the SQLite CLI just to make vector search work.

For Docker, the build stage must include a C toolchain. The checked-in `Dockerfile` uses Debian trixie for both build and runtime and keeps `CGO_ENABLED=1` enabled for this reason.

### `VECTOR_STORE=pgvector`

This is the advanced backend for vector-backed surfaces:

- grouping similarity records
- supergroup artifacts

When enabled, set:

```bash
VECTOR_STORE=pgvector
VECTOR_STORE_DSN=postgres://seer:seer@postgres:5432/seer?sslmode=disable
VECTOR_DIMENSIONS=1536
```

Important: autofix run state and project preferences still live in SQLite even when `pgvector` is enabled.

## Sentry observability

`faux-seer` can report its own errors and performance to Sentry using `sentry-go`.

Supported environment variables:

- `SENTRY_DSN`
- `SENTRY_SAMPLE_RATE`
- `SENTRY_TRACES_SAMPLE_RATE`
- `SENTRY_SEND_DEFAULT_PII`

When `SENTRY_DSN` is set, faux-seer enables:

- error monitoring
- tracing
- logs via `sentry-go/slog`
- request metrics via `sentry.NewMeter`

## Known limitations

Compared with real Seer:

- autofix is compatibility-oriented and persists placeholder state rather than running Seer's full agent loop
- summaries and severity are heuristic or provider-assisted, not model-parity implementations
- app state stays on SQLite even when `pgvector` is enabled
- several Seer-only behaviors remain intentionally unimplemented

## Building from source

```bash
go build ./...
go run ./cmd/faux-seer
```

## Development workflow

Useful validation commands:

```bash
go test ./...
go vet ./...
go build ./...
docker compose config
```

## Contributing

Contributions should preserve Sentry compatibility first:

- derive request and response shapes from real `seer` and `sentry` contracts
- keep auth compatible with Sentry's RPC signature format
- avoid introducing provider-specific behavior into public endpoint schemas
- update `README.md`, `ARCHITECTURE.md`, and `docs/endpoints.md` when the surface area changes
