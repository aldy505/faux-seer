# faux-seer

`faux-seer` is a self-hosted, Go-based compatibility layer for Sentry's Seer service. It implements the signed HTTP endpoints Sentry expects for the Explorer agent, autofix coding-agent state, summaries, similarity, supergroups, and severity, while delegating text generation and embeddings to configurable providers or safe local stubs.

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

### Seer URLs

Sentry builds one HTTP connection pool per Seer subsystem, and each pool reads
its own setting from `src/sentry/conf/server.py`. `SEER_DEFAULT_URL` only seeds
those settings at import time; nothing reads it at runtime.

| Sentry setting | Surface it reaches |
| --- | --- |
| `SEER_AUTOFIX_URL` | explorer chat/state/runs/repos/index, agent feature runs, autofix coding-agent state, codegen unit tests, oneshot, assisted query, investigations, issue detection, supergroups, project preferences, service-map updates, `GET /v1/models`, `POST /v1/llm/generate`, GCP verify-connection |
| `SEER_SUMMARIZATION_URL` | issue/trace summaries, fixability, feedback summaries, replay breadcrumbs, severity score |
| `SEER_GROUPING_URL` | `/v0/issues/similar-issues` and grouping-record deletion |
| `SEER_ANOMALY_DETECTION_URL` | anomaly detection endpoints and `/v1/workflows/compare/cohort` |
| `SEER_BREAKPOINT_DETECTION_URL` | `/trends/breakpoint-detector` |
| `SEER_PREVENT_AI_URL` | code review and PR-metrics endpoints |

Sentry defines `SEER_DEFAULT_URL = "http://127.0.0.1:9091"` and then derives the
rest from it (`SEER_AUTOFIX_URL = SEER_DEFAULT_URL`, `SEER_SUMMARIZATION_URL =
SEER_DEFAULT_URL`, and so on). That derivation happens when `server.py` is
imported, so reassigning `SEER_DEFAULT_URL` in your own settings module does
**not** rebind the derived names: `sentry.conf.py` begins with
`from sentry.conf.server import *`, which copies the already-computed values.
Assign every setting you need:

```python
# sentry.conf.py
SEER_DEFAULT_URL = "http://faux-seer:9091"
SEER_AUTOFIX_URL = SEER_DEFAULT_URL
SEER_SUMMARIZATION_URL = SEER_DEFAULT_URL
SEER_GROUPING_URL = SEER_DEFAULT_URL
SEER_ANOMALY_DETECTION_URL = SEER_DEFAULT_URL
SEER_BREAKPOINT_DETECTION_URL = SEER_DEFAULT_URL
SEER_PREVENT_AI_URL = SEER_DEFAULT_URL
```

`SEER_SCORING_URL` also exists in `server.py`, but nothing in Sentry reads it.
There is no environment-variable plumbing for these either — they are plain
settings, so set them in the settings module.

Using one base URL for all of them is correct because `faux-seer` serves all of
those compatibility endpoints from a single HTTP server. Sentry's development
default is already `http://127.0.0.1:9091`, which is also `faux-seer`'s default
port, so a host-run `faux-seer` needs no URL changes at all — only the shared
secret.

### Shared secret

Requests from Sentry to Seer are signed with **one** setting,
`SEER_API_SHARED_SECRET` (a string), and faux-seer must be configured with the
same value. Sentry sends:

```text
Authorization: Rpcsignature rpc0:<hex>
```

The signature is HMAC-SHA256 over the raw request body
(`src/sentry/seer/signed_seer_api.py`). Sentry also attaches an
`X-Viewer-Context` JWT signed with the same secret; faux-seer verifies the HMAC
header and ignores the JWT.

faux-seer reads its copy from `SEER_SHARED_SECRET`, falling back in order to
`SEER_RPC_SHARED_SECRET`, `SEER_API_SHARED_SECRET`, then `SHARED_SECRET`, so
reusing the Sentry variable name in faux-seer's `.env` also works. The value may
be a comma- or semicolon-separated list, matching Sentry's own list-of-secrets
style, and a request is accepted if any secret in the list verifies.

If `SEER_API_SHARED_SECRET` is empty, Sentry sends **no** `Authorization` header
at all and logs `seer.unsigned_request`. A faux-seer with a shared secret
configured then answers 401 to every call, so configure the pair together — or
leave the secret unset on both sides to skip verification in local development.

`SEER_RPC_SHARED_SECRET` in *Sentry* is the opposite direction: it is a list of
secrets Sentry uses to validate `Rpcsignature`-signed requests that Seer sends
*back* to Sentry (cross-region RPC). It does not authenticate requests to
faux-seer, so setting it on the Sentry side does not affect this integration.

### Feature flags and organization settings

URLs alone are not enough: Sentry decides whether to call Seer with feature
flags. Every Seer surface first checks

```python
has_seer_access(organization, actor)  # src/sentry/seer/seer_setup.py
```

which requires the `organizations:gen-ai-features` flag to be on **and** the
organization's "Enable generative AI features" toggle
(`sentry:hide_ai_features`, exposed in organization settings) to be off.
Individual surfaces add their own gate:

| Surface | Additional requirement |
| --- | --- |
| explorer chat and agent runs | `organizations:seer-explorer`, plus open team membership on the organization |
| periodic explorer indexing | `organizations:seer-explorer-index` (or a Seer plan: `organizations:seat-based-seer-enabled` / `organizations:seer-added`) together with `organizations:gen-ai-features`, and no AI-feature opt-out |
| autofix autotrigger | `has_seer_access` only |
| code review | `organizations:code-review-beta` or `organizations:seat-based-seer-enabled`, plus per-repository code review enablement |
| investigations | `organizations:investigations` plus open team membership |
| issue detection | `organizations:ai-issue-detection` |
| feedback summaries | `has_seer_access` only |
| replay breadcrumbs | `organizations:session-replay` |
| similarity grouping | project option `sentry:similarity_backfill_completed` (rate limits and killswitches can still suppress calls) |
| severity score | project flag `projects:first-event-severity-calculation` |
| assisted query | non-empty `SEER_AUTOFIX_URL` on top of `has_seer_access` |
| anomaly detection, breakpoints, supergroups, offboarding, project preferences | no Seer-specific flag; they run when their owning endpoint or task runs |

Flag definitions and defaults live in `src/sentry/features/temporary.py` and
`src/sentry/features/permanent.py`. Most Seer flags use
`FeatureHandlerStrategy.FLAGPOLE`, and `FeatureManager.has` falls back to
`settings.SENTRY_FEATURES` when no handler resolves a flag, so a self-hosted
install can turn them on from its settings module:

```python
# sentry.conf.py
SENTRY_FEATURES = {
    "organizations:gen-ai-features": True,
    "organizations:seer-explorer": True,
    "organizations:seer-explorer-index": True,
}
```

Flags backed by plan or seat data (`organizations:seer-added`,
`organizations:seat-based-seer-enabled`) resolve through their own handlers
instead of that fallback. In the UI, granting features per organization is the
usual route, and the AI toggle above is part of organization settings.

## Implemented compatibility surface

Implemented routes are exactly the paths Sentry's Seer client can call. Health:

- `GET /health`
- `GET /health/live`
- `GET /health/ready`

Agent and explorer runs:

- `POST /v1/automation/agent/feature/run`
- `POST /v1/automation/explorer/chat`
- `POST /v1/automation/explorer/state`
- `POST /v1/automation/explorer/state/pr`
- `POST /v1/automation/explorer/runs/by-ids`
- `POST /v1/automation/explorer/repos`
- `POST /v1/automation/explorer/update`
- `POST /v1/automation/explorer/index`
- `POST /v1/automation/explorer/index/org-repo-knowledge`
- `POST /v1/automation/explorer/index/org-project-knowledge`
- `POST /v1/automation/explorer/index/sentry-knowledge`
- `POST /v1/automation/explorer/export-indexes`
- `POST /v1/explorer/service-map/update`

Autofix and codegen:

- `POST /v1/automation/autofix/coding-agent/state/set`
- `POST /v1/automation/autofix/coding-agent/state/update`
- `POST /v1/automation/codegen/unit-tests`
- `POST /v1/automation/oneshot/run`

Summaries:

- `POST /v1/automation/summarize/issue`
- `POST /v1/automation/summarize/trace`
- `POST /v1/automation/summarize/fixability`
- `POST /v1/automation/summarize/feedback/spam-detection`
- `POST /v1/automation/summarize/feedback/labels`
- `POST /v1/automation/summarize/feedback/title`
- `POST /v1/automation/summarize/feedback/label-groups`
- `POST /v1/automation/summarize/feedback/summarize`
- `POST /v1/automation/summarize/replay/breadcrumbs/start`
- `POST /v1/automation/summarize/replay/breadcrumbs/state`
- `POST /v1/automation/summarize/replay/breadcrumbs/delete`

Investigations and issue detection:

- `POST /v1/automation/investigations`
- `POST /v1/automation/investigations/{run_id}/commands`
- `GET /v1/automation/investigations/{run_id}`
- `POST /v1/automation/issue-detection/analyze`
- `GET /v1/automation/issue-detection/check-budget/{org_id}`

Assisted query:

- `POST /v1/assisted-query/state`
- `POST /v1/assisted-query/start`
- `POST /v1/assisted-query/translate`
- `POST /v1/assisted-query/translate-agentic`
- `POST /v1/assisted-query/create-cache`

Anomaly detection, breakpoints, and workflows:

- `POST /v1/anomaly-detection/detect`
- `POST /v1/anomaly-detection/alert-data`
- `POST /v1/anomaly-detection/store`
- `POST /v1/anomaly-detection/delete-alert-data`
- `POST /v1/workflows/compare/cohort`
- `POST /trends/breakpoint-detector`

Code review, offboarding, and PR metrics:

- `POST /v1/code_review/check/rerun`
- `POST /v1/code_review/review-request`
- `POST /v1/code_review/pr-closed`
- `POST /v1/offboarding/repository`
- `POST /v1/pr-metrics/delegated-agent-match`
- `POST /v1/pr-metrics/pr-close-judge`

Grouping, supergroups, severity, models, and monitoring providers:

- `POST /v0/issues/similar-issues`
- `GET /v0/issues/similar-issues/grouping-record/delete/{project_id}`
- `POST /v0/issues/similar-issues/grouping-record/delete-by-hash`
- `POST /v0/issues/supergroups/cluster-lightweight`
- `POST /v0/issues/supergroups/get`
- `POST /v0/issues/supergroups/get-by-group-ids`
- `POST /v0/issues/severity-score`
- `GET /v1/models`
- `POST /v1/llm/generate`
- `POST /v1/monitoring-providers/gcp/verify-connection`

Project preference maintenance:

- `POST /v1/project-preference/remove-repository`
- `POST /v1/project-preference/bulk-remove-repositories`
- `POST /v1/project-preference/remove-handoffs-for-integration`

See `docs/endpoints.md` for request and response examples.

See `docs/status.md` for the current implementation status, known gaps, and recommended next steps.

## Configuration

The repo targets the Go version declared in `go.mod` (`go 1.27` at the time of writing).

Core environment variables:

| Variable | Purpose |
| --- | --- |
| `ADDR` | HTTP bind address, default `:9091` |
| `LOG_LEVEL` | Logging level; `info` emits HTTP access logs, while `warn`/`error` suppress them |
| `DATABASE_PATH` | SQLite database path, default `data/faux-seer.db` |
| `SEER_SHARED_SECRET` | Shared HMAC secret for Sentry-compatible RPC signing |
| `VECTOR_STORE` | `sqlitevec` or `pgvector` |
| `VECTOR_STORE_DSN` | Required when `VECTOR_STORE=pgvector` |
| `VECTOR_DIMENSIONS` | Embedding/vector width for pgvector storage |
| `SIMILARITY_THRESHOLD` | Default nearest-neighbor threshold |
| `LLM_MODEL` | Comma-separated model list, rotated round-robin per request |
| `EMBEDDING_MODEL` | Comma-separated model list, rotated round-robin per request |
| `GITHUB_TOKEN` | GitHub personal-access token; when set, explorer repos, org-repo indexing, codegen, and delegated-agent-match become real |
| `GITHUB_BASE_URL` | GitHub API base URL, default `https://api.github.com` |
| `GITLAB_TOKEN` | GitLab token (provider not yet implemented; endpoints fall back to simulated) |
| `GITLAB_BASE_URL` | GitLab API base URL, default `https://gitlab.com/api/v4` |
| `GITEA_TOKEN` | Gitea token (provider not yet implemented) |
| `GITEA_BASE_URL` | Gitea API base URL |
| `BITBUCKET_TOKEN` | Bitbucket token (provider not yet implemented) |
| `BITBUCKET_BASE_URL` | Bitbucket API base URL, default `https://api.bitbucket.org/2.0` |
| `GCP_SERVICE_ACCOUNT_JSON` | Inline JSON or filesystem path to a GCP service-account key; when set, GCP verification becomes real |
| `CODE_INDEX_CHUNK_SIZE` | Maximum runes per indexed code chunk, default `1000` |
| `CODE_INDEX_CHUNK_OVERLAP` | Overlap between consecutive chunks, default `200` |

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

`LLM_MODEL` and `EMBEDDING_MODEL` accept comma-separated lists. Every provider request picks the next model in the list, so a multi-model list spreads load across models instead of pinning one:

```bash
LLM_MODEL=gpt-4.1-mini,gpt-4.1-nano
EMBEDDING_MODEL=text-embedding-3-small
```

The similarity response's `model_used` reports the first configured embedding model, not necessarily the model that served that request.

Example OpenAI-compatible setup:

```bash
LLM_PROVIDER=openai
LLM_API_KEY=sk-...
LLM_MODEL=gpt-4.1-mini,gpt-4.1-nano

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

Important: autofix and explorer run state still live in SQLite even when `pgvector` is enabled.

## What is real versus simulated

Most endpoints now perform real work when the right credentials are configured. A few remain simulated because their dependencies are internal to Seer or require infrastructure not available externally.

**Real when configured:**

- **Explorer chat** creates an async run, calls the LLM, and persists the reply. The code index augments replies with repository chunks when a repository provider and embedding client are available.
- **Explorer repos** lists stored repository preferences or the configured provider's repositories.
- **Explorer index/org-repo-knowledge** fetches, chunks, embeds, and stores repository code in the vector backend.
- **Summarize/trace**, **summarize/fixability**, and all **feedback/* endpoints** call the LLM.
- **Replay breadcrumbs start/state/delete** persist and LLM-summarize via SQLite.
- **Severity score** calls the LLM and falls back to a deterministic heuristic on failure.
- **LLM generate** and **oneshot/run** call the LLM.
- **Investigations** create/command/get are persisted runs with an LLM-backed workflow step.
- **Assisted query start/translate/translate-agentic** call the LLM, grounded in code-index context when available.
- **Codegen unit-tests** fetches the PR diff and calls the LLM when a repository provider is configured.
- **Autofix coding-agent state** lazy-creates runs on demand and advances the agent via a background LLM call.
- **Agent feature/run** creates a persisted run and calls the LLM.
- **Offboarding/repository** drops the repository's code index.
- **PR metrics/delegated-agent-match** looks up autofix runs by provider+PR or PR URL and returns 200 only on a match.
- **Project preference** endpoints persist and query per-organization repository removals.
- **GCP verify-connection** performs real service-account JWT verification when `GCP_SERVICE_ACCOUNT_JSON` is set.

**Still simulated (by design):**

- `explorer/index`, `explorer/index/org-project-knowledge`, `explorer/index/sentry-knowledge`, `explorer/export-indexes`, `explorer/service-map/update` — the request carries no repository identity or the target is internal to Seer.
- `issue-detection/analyze` (202 ack) and `issue-detection/check-budget` — no event-processing pipeline.
- `anomaly-detection/*`, `workflows/compare/cohort`, `trends/breakpoint-detector` — require a timeseries backend.
- `code_review/*`, `pr-metrics/pr-close-judge` — out of scope or callback-only.
- `supergroups/cluster-lightweight` — requires a grouping model.
- `assisted-query/create-cache` — internal prompt-caching optimization.
- `monitoring-providers/gcp/verify-connection` — simulated when `GCP_SERVICE_ACCOUNT_JSON` is unset.

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

Separately from Sentry observability, faux-seer emits one structured HTTP access log line per request to stdout. Each record includes:

- method
- full request URL (path plus query string)
- request headers
- request body
- response status
- duration in milliseconds

Startup, generic errors, and these access records are the only log lines the server emits.

## Known limitations

Compared with real Seer:

- Only GitHub is implemented as a repository provider; GitLab, Gitea, and Bitbucket are recognised but not implemented (endpoints fall back to simulated responses).
- Autofix advances the coding agent with a single LLM call per state-set rather than Seer's multi-turn tool-use loop.
- Explorer chat replies are grounded in code-index chunks when available, but there is no interactive exploration or tool-use loop.
- Explorer index/org-project-knowledge, index/sentry-knowledge, export-indexes, and service-map/update remain simulated because their targets are internal to Seer.
- App state stays on SQLite even when `pgvector` is enabled.
- Several Seer-only behaviors (anomaly detection, breakpoint detection, cohort comparison, code review, issue detection) remain intentionally simulated.

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
