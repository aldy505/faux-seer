# Providers

## Sentry SDK configuration

For faux-seer's own observability, set:

```env
SENTRY_DSN=https://<public>@o<org>.ingest.sentry.io/<project>
SENTRY_SAMPLE_RATE=1.0
SENTRY_TRACES_SAMPLE_RATE=1.0
SENTRY_SEND_DEFAULT_PII=false
```

This enables Sentry errors, tracing, logs, and metrics for the faux-seer service itself.

## Stub

Use `LLM_PROVIDER=stub` and `EMBEDDING_PROVIDER=stub` for local compatibility testing without external APIs.

Example:

```env
LLM_PROVIDER=stub
EMBEDDING_PROVIDER=stub
VECTOR_STORE=sqlitevec
```

Notes:

- `sqlitevec` uses the real `sqlite-vec` extension through `github.com/asg017/sqlite-vec-go-bindings/cgo`.
- Builds must keep `CGO_ENABLED=1`.
- The packaged Docker image already includes the required Debian build dependencies in the builder stage; no extra `sqlite3` CLI install is required for runtime vector search.

## OpenAI-compatible

Set `LLM_PROVIDER=openai|openrouter|custom` and `LLM_BASE_URL` as needed. For OpenRouter, set `HTTP_REFERER` because the upstream API expects it.

OpenAI example:

```env
LLM_PROVIDER=openai
LLM_API_KEY=sk-...
LLM_MODEL=gpt-4.1-mini
EMBEDDING_PROVIDER=openai
EMBEDDING_API_KEY=sk-...
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_DIMENSIONS=1536
VECTOR_DIMENSIONS=1536
```

OpenRouter example:

```env
LLM_PROVIDER=openrouter
LLM_BASE_URL=https://openrouter.ai/api/v1
LLM_API_KEY=sk-or-...
LLM_MODEL=anthropic/claude-3.7-sonnet
EMBEDDING_PROVIDER=custom
EMBEDDING_BASE_URL=https://openrouter.ai/api/v1
EMBEDDING_API_KEY=sk-or-...
HTTP_REFERER=https://your-app.example
```

## Anthropic

Set `LLM_PROVIDER=anthropic`, `LLM_API_KEY`, and optionally `LLM_BASE_URL`.

Example:

```env
LLM_PROVIDER=anthropic
LLM_API_KEY=sk-ant-...
LLM_MODEL=claude-3-7-sonnet-latest
EMBEDDING_PROVIDER=stub
```

## pgvector

Set `VECTOR_STORE=pgvector`, `VECTOR_STORE_DSN`, and `VECTOR_DIMENSIONS` to match your embedding model. Faux-seer will create the `vector` extension plus its grouping/supergroup tables on startup.

Example:

```env
VECTOR_STORE=pgvector
VECTOR_STORE_DSN=postgres://postgres:postgres@pgvector:5432/faux_seer?sslmode=disable
VECTOR_DIMENSIONS=1536
```

Notes:

- `pgvector` currently backs similarity/grouping and supergroup storage only.
- Autofix runs and project preferences remain in the local SQLite app database.
- The Postgres user must be able to run `CREATE EXTENSION IF NOT EXISTS vector`.
