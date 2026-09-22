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

`LLM_MODEL` and `EMBEDDING_MODEL` take comma-separated lists. Requests rotate round-robin through the
configured models, so listing several models spreads provider load:

```env
LLM_MODEL=gpt-4.1-mini,gpt-4.1-nano
EMBEDDING_MODEL=text-embedding-3-small
```

OpenAI example:

```env
LLM_PROVIDER=openai
LLM_API_KEY=sk-...
LLM_MODEL=gpt-4.1-mini,gpt-4.1-nano
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
LLM_MODEL=claude-3-7-sonnet-latest,claude-3-5-haiku-latest
EMBEDDING_PROVIDER=stub
```

## Repository providers

Set `GITHUB_TOKEN` (and optionally `GITHUB_BASE_URL`) to enable repository-backed endpoints: explorer repos, org-repo indexing, codegen unit tests, offboarding, and delegated-agent-match.

```env
GITHUB_TOKEN=ghp_...
GITHUB_BASE_URL=https://api.github.com
```

When the token is unset, `NewProvider("github")` returns `ErrNotConfigured` and repository-backed endpoints fall back to simulated responses (empty lists or acks). This is by design: no customer repository can be accessed without credentials.

Other providers Sentry can report (`gitlab`, `gitea`, `bitbucket`) are recognised but not yet implemented. Setting their token causes `NewProvider` to return `ErrNotConfigured` with a message like `"the gitlab provider is not implemented"`, so handlers fall back to simulated responses.

## Code index

The code index service (`internal/codeindex`) fetches, chunks, embeds, and stores repository source text so explorer runs can search it semantically.

Configuration:

```env
CODE_INDEX_CHUNK_SIZE=1000
CODE_INDEX_CHUNK_OVERLAP=200
```

- `CODE_INDEX_CHUNK_SIZE`: maximum runes per indexed chunk (default `1000`).
- `CODE_INDEX_CHUNK_OVERLAP`: overlap between consecutive chunks (default `200`).

Indexing behavior:

- Fetches the default branch when `ref` is empty.
- Walks the repository tree via the git provider's `ReadTree`.
- Skips binary files (detected by NUL byte in the first block) and files larger than 1 MiB.
- Chunks text into `CODE_INDEX_CHUNK_SIZE`-rune slices with `CODE_INDEX_CHUNK_OVERLAP` overlap.
- Embeds chunks via the configured embedding client (`EMBEDDING_PROVIDER`).
- Stores chunks in the configured vector backend (`sqlitevec` or `pgvector`).
- Replaces every previously stored chunk for the repository (full re-index).
- Repositories removed via project preferences are skipped.

Search embeds the query and returns the closest indexed code chunks from the vector store.

## GCP service account

For real GCP connection verification, set `GCP_SERVICE_ACCOUNT_JSON` to an inline JSON string or a filesystem path to a service-account key file:

```env
GCP_SERVICE_ACCOUNT_JSON=/path/to/service-account.json
```

When configured, the verification endpoint loads the service account, exchanges a signed JWT for an OAuth2 access token, and checks project access via Cloud Resource Manager and Service Usage APIs.

When unset, the endpoint returns `"connected"` for every project without performing real verification.

## pgvector

Set `VECTOR_STORE=pgvector`, `VECTOR_STORE_DSN`, and `VECTOR_DIMENSIONS` to match your embedding model. Faux-seer will create the `vector` extension plus its grouping/supergroup/code-chunk tables on startup.

Example:

```env
VECTOR_STORE=pgvector
VECTOR_STORE_DSN=postgres://postgres:postgres@pgvector:5432/faux_seer?sslmode=disable
VECTOR_DIMENSIONS=1536
```

Notes:

- `pgvector` backs similarity/grouping, supergroup storage, and code-index chunks.
- Autofix and explorer runs remain in the local SQLite app database.
- The Postgres user must be able to run `CREATE EXTENSION IF NOT EXISTS vector`.
