# Endpoints

All protected endpoints use the Sentry-compatible authorization scheme:

```text
Authorization: Rpcsignature rpc0:<hex-hmac-sha256-of-raw-body>
```

If `SEER_SHARED_SECRET` is unset, faux-seer skips signature verification for local development.

## Signing requests

For local testing, you can compute the header from the exact raw JSON payload:

```bash
export BODY='{"message":"panic: nil pointer dereference","has_stacktrace":1,"handled":false}'
export AUTH="Rpcsignature rpc0:$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SEER_SHARED_SECRET" -binary | xxd -p -c 256)"
curl -sS \
  -H "Content-Type: application/json" \
  -H "Authorization: $AUTH" \
  -d "$BODY" \
  http://127.0.0.1:9091/v0/issues/severity-score
```

Health endpoints do not require authorization.

## Common error responses

Protected endpoints may return:

- `200 OK` for successful requests
- `400 Bad Request` when JSON decoding or request validation fails
- `401 Unauthorized` when the HMAC header is missing or invalid
- `404 Not Found` for routes outside the compatibility surface

Error payload shape:

```json
{"error":"decode severity request: unexpected end of JSON input"}
```

---

## Health (no auth)

### `GET /health`

### `GET /health/live`

### `GET /health/ready`

Response:

```json
{"status":"ok"}
```

---

## Agent and explorer runs

### `POST /v1/automation/agent/feature/run`

Real. Creates a persisted run via the run manager and calls the LLM for a short result. Retried requests with the same `ExternalIdempotencyKey` reuse the existing run.

Request example:

```json
{"feature_id": "explorer", "payload": {"query": "hello"}, "external_idempotency_key": "exp-123"}
```

Response:

```json
{"success":true,"run_id":1}
```

### `POST /v1/automation/explorer/chat`

Real. Creates an `ExplorerRunRecord` with `status: "processing"`, persists it, and dispatches a background `chatTask` via the run manager. The task calls the LLM, optionally augmenting the prompt with code-index chunks when the query looks code-related, and appends user and assistant blocks. The HTTP response returns immediately. Sentry polls `/explorer/state` for the completed result.

Request example:

```json
{
  "organization_id": 1,
  "query": "What are my slowest DB queries?",
  "page_name": "/issues/:groupId/",
  "on_page_context": "Trace ID: 467c1436d96a4aab9a4fa01905ab7ee9",
  "user_org_context": {
    "user_id": 1
  }
}
```

Response:

```json
{"run_id":1,"has_explorer_index":true,"has_org_project_context":true}
```

### `POST /v1/automation/explorer/state`

Real. Returns the stored run state. Sentry builds a pydantic `SeerRunState` requiring `run_id`, `blocks[]`, `status`, and `updated_at`. The `status` field is a lowercase enum: `"processing"`, `"completed"`, `"error"`, or `"not_started"`. When a run fails (e.g. on process shutdown), the state includes a top-level `failure_reason` field set to `"shutdown"`.

Request example:

```json
{"organization_id": 1, "run_id": 1}
```

Response (completed):

```json
{
  "session": {
    "run_id": 1,
    "blocks": [
      {
        "id": "block-1",
        "message": {
          "role": "user",
          "content": "What are my slowest DB queries?"
        },
        "timestamp": "2026-05-12T05:39:26Z",
        "loading": false
      },
      {
        "id": "block-2",
        "message": {
          "role": "assistant",
          "content": "Stub provider response:\n\nWhat are my slowest DB queries?"
        },
        "timestamp": "2026-05-12T05:39:26Z",
        "loading": false
      }
    ],
    "status": "completed",
    "updated_at": "2026-05-12T05:39:26Z",
    "owner_user_id": 1,
    "repo_pr_states": {}
  }
}
```

Response (error with failure_reason):

```json
{
  "session": {
    "run_id": 1,
    "blocks": [],
    "status": "error",
    "updated_at": "2026-05-12T05:39:26Z",
    "failure_reason": "shutdown",
    "owner_user_id": 1,
    "repo_pr_states": {}
  }
}
```

### `POST /v1/automation/explorer/state/pr`

Real. Returns the stored run state for a PR-scoped session, or `null` if no session exists.

Request example:

```json
{"organization_id": 1, "provider": "github", "pr_id": 123}
```

Response:

```json
{"session": null}
```

### `POST /v1/automation/explorer/runs/by-ids`

Real. Returns live run summaries keyed by run id. Unknown run ids are omitted.

Request example:

```json
{"run_ids": [1, 2]}
```

Response:

```json
{
  "data": {
    "1": {
      "run_id": 1,
      "status": "completed",
      "title": "What are my slowest DB queries?",
      "last_triggered_at": "2026-05-12T05:39:26Z",
      "created_at": "2026-05-12T05:39:26Z",
      "user_id": 1
    }
  }
}
```

### `POST /v1/automation/explorer/repos`

Real when a repository provider is configured. Returns the organization's stored repository preferences, or the configured provider's repositories via `Provider.ListRepos`. Without a configured provider, the empty list is the simulated answer.

Request example:

```json
{"run_id": 1, "organization_id": 1}
```

Response:

```json
{
  "repos": [
    {
      "id": "12345",
      "name": "acme/app",
      "provider": "github",
      "owner": "acme",
      "external_id": "12345",
      "default_branch": "main",
      "read_access": true,
      "write_access": true
    }
  ]
}
```

### `POST /v1/automation/explorer/update`

Real. Mutates run state for interrupts, user input responses, and created PRs.

Request example:

```json
{"organization_id": 1, "run_id": 1, "payload": {"type": "interrupt"}}
```

Response:

```json
{"run_id":1}
```

### `POST /v1/automation/explorer/index`

SIMULATED. The request carries only org and project ids, so faux-seer has no repository identity to fetch. Sentry sends repository details to `/explorer/index/org-repo-knowledge`, which does index them.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/index/org-repo-knowledge`

Real when a repository provider and embedding client are configured. Fetches, chunks, embeds, and stores repository code via `codeindex.IndexRepo` in the background. Skips repositories the org has removed via project preferences. Returns `{"success": true}` immediately; indexing runs asynchronously.

Request example:

```json
{
  "org_id": 1,
  "repositories": [
    {"provider": "github", "owner": "acme", "name": "app", "default_branch": "main"}
  ]
}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/index/org-project-knowledge`

SIMULATED. The request carries only org and project ids without repository identity; real project-knowledge indexing requires Seer-internal project metadata not available to faux-seer.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/index/sentry-knowledge`

SIMULATED. Sentry's documentation corpus is not reachable from faux-seer.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/export-indexes`

SIMULATED. The export format and its destination are internal to Seer.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/explorer/service-map/update`

SIMULATED. faux-seer stores no service map, so the snapshot is discarded; Sentry checks only the HTTP status.

Request example:

```json
{"any_key": "any_value"}
```

Response:

```json
{"success":true}
```

---

## Autofix and codegen

### `POST /v1/automation/autofix/coding-agent/state/set`

Real. Persists coding-agent state onto a run. When the run does not yet exist, the service creates it on demand with the caller-supplied id (because Sentry reports state for runs Seer already created). A background agent-advancement task is started that calls the LLM once to advance the agent.

Request example:

```json
{
  "run_id": 1,
  "coding_agent_states": [
    {
      "id": "agent-1",
      "status": "RUNNING",
      "branch_name": "autofix/typeerror-checkout"
    }
  ]
}
```

Response:

```json
{"run_id":1,"status":"success"}
```

Unknown `run_id` is lazily created. On process shutdown, in-flight advancement tasks persist `status: "error"` with `failure_reason: "shutdown"`.

### `POST /v1/automation/autofix/coding-agent/state/update`

Real. Merges an update into one agent entry, locating the owning run by scanning all persisted runs for the agent id.

Request example:

```json
{
  "agent_id": "agent-1",
  "updates": {
    "status": "COMPLETED",
    "pull_request_url": "https://github.com/acme/app/pull/123"
  }
}
```

Response:

```json
{"run_id":1,"status":"success"}
```

Unknown `agent_id` returns `{"run_id":0,"status":"error","message":"agent not found"}` with status `200`.

### `POST /v1/automation/codegen/unit-tests`

Real when a repository provider is configured. Fetches the PR diff for the changed file and asks the LLM to generate unit tests. Without a configured provider, returns a simulated success.

Request example:

```json
{
  "repo": {"provider": "github", "owner": "acme", "name": "app"},
  "pr_id": 123,
  "file_path": "src/foo.py"
}
```

Response:

```json
{"success":true,"tests":"import pytest\n..."}
```

### `POST /v1/automation/oneshot/run`

Real. Calls the LLM and returns `{"result": {...}}`. The result keys vary by `oneshot_id`: `"conversation_title"` returns `{"title": ...}`, `"agent_question"` returns `{"answer": ...}`. Unknown ids default to `{"answer": ...}`.

Request example:

```json
{"oneshot_id": 1, "payload": {"type": "test"}}
```

Response:

```json
{"result":{"answer":"..."}}
```

---

## Summaries

### `POST /v1/automation/summarize/issue`

Real. Calls the configured LLM for prose.

Request example:

```json
{"group_id": 1, "issue": {"title": "TypeError"}, "trace_tree": null}
```

Response:

```json
{
  "group_id": 1,
  "headline": "TypeError in foo()",
  "whats_wrong": "A nil pointer was dereferenced.",
  "trace": "main → foo → bar",
  "possible_cause": "Uninitialized variable.",
  "scores": {"likelihood": 0.8, "impact": 0.9}
}
```

### `POST /v1/automation/summarize/trace`

Real. Calls the LLM with trace context from the request and populates all fields of `SummarizeTraceResponse`. Falls back to a deterministic heuristic when the LLM fails.

Request example:

```json
{"trace_id": "abc123", "trace": null}
```

Response:

```json
{
  "trace_id": "abc123",
  "summary": "Trace spans indicate slow DB queries.",
  "key_observations": ["High latency in SELECT"],
  "performance_characteristics": {"p99_ms": 1200},
  "suggested_investigations": ["Check indexing"]
}
```

### `POST /v1/automation/summarize/fixability`

Real. Calls the LLM and returns a fixability assessment with the same shape as issue summaries.

Request example:

```json
{"group_id": 1}
```

Response:

```json
{
  "group_id": 1,
  "headline": "TypeError in foo()",
  "whats_wrong": "A nil pointer was dereferenced.",
  "trace": "main → foo → bar",
  "possible_cause": "Uninitialized variable.",
  "scores": {"likelihood": 0.8, "impact": 0.9}
}
```

### `POST /v1/automation/summarize/feedback/spam-detection`

Real. Calls the LLM with a boolean classification prompt. Returns HTTP 400 for malformed requests or LLM failures.

Request example:

```json
{"organization_id": 1, "feedback_message": "Great app!"}
```

Response:

```json
{"is_spam":false}
```

### `POST /v1/automation/summarize/feedback/labels`

Real. Calls the LLM to produce a JSON array of category labels. Returns HTTP 400 for malformed requests or LLM failures.

Request example:

```json
{"organization_id": 1, "feedback_message": "The login page is broken."}
```

Response:

```json
{"data":{"labels":["bug","ui"]}}
```

### `POST /v1/automation/summarize/feedback/title`

Real. Calls the LLM to produce a title. Falls back to the deterministic `DeriveFeedbackTitle` heuristic when the LLM returns empty.

Request example:

```json
{"organization_id": 1, "feedback_message": "The login page is broken."}
```

Response:

```json
{"title":"The login page is broken."}
```

### `POST /v1/automation/summarize/feedback/label-groups`

Real. Calls the LLM to group labels with associated labels. Returns one entry per requested label.

Request example:

```json
{"labels": ["bug", "feature"]}
```

Response:

```json
{"data":[{"primaryLabel":"bug","associatedLabels":["regression"]},{"primaryLabel":"feature","associatedLabels":["enhancement"]}]}
```

### `POST /v1/automation/summarize/feedback/summarize`

Real. Calls the LLM to summarize a list of feedback messages. Returns HTTP 400 for malformed requests or LLM failures.

Request example:

```json
{"feedbacks": [{"message": "App crashes"}, {"message": "Slow loading"}]}
```

Response:

```json
{"data":"Users report crashes on startup and slow page loading."}
```

### `POST /v1/automation/summarize/replay/breadcrumbs/start`

Real. Validates the request, upserts a `replay_breadcrumb_summaries` row with `status: "processing"`, calls the LLM with a synthesized breadcrumb narrative (the request carries `replay_id` and `num_segments` but no raw segments), stores the summary, and returns it with `status: "completed"`.

Request example:

```json
{"replay_id": 1, "num_segments": 3}
```

Response:

```json
{"created_at":"2026-05-12T05:39:26Z","status":"completed","num_segments":3,"data":{"summary":"Breadcrumbs summarized.","time_ranges":[]}}
```

### `POST /v1/automation/summarize/replay/breadcrumbs/state`

Real. Reads the stored replay breadcrumb summary from SQLite. When no summary exists for the given `replay_id`, returns `status: "not_started"`.

Request example:

```json
{"replay_id": 1, "organization_id": 1, "project_id": 1}
```

Response:

```json
{"created_at":"2026-05-12T05:39:26Z","status":"completed","num_segments":null,"data":{"summary":"Breadcrumbs summarized.","time_ranges":[]}}
```

### `POST /v1/automation/summarize/replay/breadcrumbs/delete`

Real. Removes stored replay breadcrumb summaries from SQLite and returns `{"success": true}`.

Request example:

```json
{"replay_ids": [1, 2], "organization_id": 1, "project_id": 1}
```

Response:

```json
{"success":true}
```

---

## Investigations

### `POST /v1/automation/investigations`

Real. Creates a persisted run via `runs.Service.Start` with an idempotency key derived from `requestId`. The background task calls the LLM with an investigation prompt and merges the answer into the run projection. Sentry validates `runId >= 1`, `created` bool, and `projection` dict.

Request example:

```json
{"requestId": "550e8400-e29b-41d4-a716-446655440000", "investigationId": "inv-1", "source": "seer", "activeTimeBudgetSeconds": 30}
```

Response:

```json
{"runId":1,"created":true,"projection":{}}
```

### `POST /v1/automation/investigations/{run_id}/commands`

Real. Validates `requestId` and `expectedWorkflowVersion`, records the command via `AcceptCommand` (deduplicating by request id), increments the workflow version, and returns the accepted command with the updated version. `requestId` is echoed when it parses as UUID, else a fresh v4 is generated.

Request example:

```json
{"requestId": "550e8400-e29b-41d4-a716-446655440000", "expectedWorkflowVersion": 1, "command": {"type": "step"}}
```

Response:

```json
{"runId":1,"requestId":"550e8400-e29b-41d4-a716-446655440000","accepted":true,"duplicate":false,"workflowVersion":1,"projection":{}}
```

### `GET /v1/automation/investigations/{run_id}`

Real. Reads the persisted run and returns the projection, which contains the run summary plus the most recent accepted command.

Response:

```json
{"runId":1,"created":true,"projection":{}}
```

---

## Issue detection

### `POST /v1/automation/issue-detection/analyze`

SIMULATED. Real issue detection needs the live event pipeline that feeds it, which is not reachable from faux-seer. Returns **202** (not 200). The response body is never parsed by Sentry.

Request example:

```json
{"traces": [{"trace_id": "abc123"}], "organization_id": 1, "project_id": 1, "org_slug": "acme", "plan_tier": "business"}
```

Response (HTTP 202):

```json
{"success":true}
```

### `GET /v1/automation/issue-detection/check-budget/{org_id}?plan_tier=`

SIMULATED. faux-seer keeps no detection budget and always reports budget available; Sentry fails open when the value is absent.

Response:

```json
{"has_budget":true}
```

---

## Assisted query

### `POST /v1/assisted-query/start`

Real. Creates a persisted run via `runs.Service.Start` and dispatches a background `answerTask` that calls the LLM, grounded in code-index context when the organization has an index. Sentry's outbox reads `run_id`.

Request example:

```json
{"organization_id": 1, "query": "Show me slow database queries", "project_ids": [1, 2]}
```

Response:

```json
{"run_id":1}
```

### `POST /v1/assisted-query/state`

Real. Reads the persisted run and returns the session with `status`, `current_step`, `completed_steps`, and `final_response`.

Response:

```json
{
  "session": {
    "run_id": 1,
    "status": "completed",
    "current_step": null,
    "completed_steps": [],
    "updated_at": "2026-05-12T05:39:26Z",
    "final_response": "The slowest queries are...",
    "unsupported_reason": null
  }
}
```

### `POST /v1/assisted-query/translate`

Real. Calls the LLM with the natural-language question and parses the model's JSON response into a list of `Query` objects. Code-index context is included when available. Consumer reads `responses` and `unsupported_reason`.

Request example:

```json
{"organization_id": 1, "query": "Show me slow database queries", "project_ids": [1, 2]}
```

Response:

```json
{"responses":[{"query":"avg-span(duration):avg:>500","stats_period":"14d","sort":"avg","group_by":["project"],"visualization":"line"}],"unsupported_reason":null}
```

### `POST /v1/assisted-query/translate-agentic`

Real. Uses the same translation logic as `translate`.

Response:

```json
{"responses":[],"unsupported_reason":null}
```

### `POST /v1/assisted-query/create-cache`

SIMULATED. Prompt caching is an internal Seer optimization; Sentry only checks the HTTP status and never reads the body.

Response:

```json
{"success":true}
```

---

## Anomaly detection, breakpoints, workflows

SIMULATED: all endpoints in this section return empty-result shapes. Real anomaly detection requires a trained timeseries model and historical data store, neither of which faux-seer provides.

### `POST /v1/anomaly-detection/detect`

Empty `timeseries` means no anomalies.

Response:

```json
{"success":true,"timeseries":[]}
```

### `POST /v1/anomaly-detection/alert-data`

Empty `data` means no threshold data.

Response:

```json
{"success":true,"data":[]}
```

### `POST /v1/anomaly-detection/store`

Consumer raises only if `success` is falsy.

Response:

```json
{"success":true}
```

### `POST /v1/anomaly-detection/delete-alert-data`

Consumer requires `success` is true.

Response:

```json
{"success":true}
```

### `POST /v1/workflows/compare/cohort`

SIMULATED. Cohort comparison needs a metric/events backend, so the result list is always empty; the consumer iterates `results`.

Response:

```json
{"results":[]}
```

### `POST /trends/breakpoint-detector`

SIMULATED. Breakpoint detection needs a statistical timeseries backend, so no breakpoints are ever reported; an empty `data` means none detected.

Response:

```json
{"data":[]}
```

---

## Code review, offboarding, PR metrics

SIMULATED: `code_review/check/rerun`, `code_review/review-request`, and `code_review/pr-closed` are simulated acks. A full code-review agent is out of scope for faux-seer. The consumer discards the body and checks only the HTTP status.

### `POST /v1/code_review/check/rerun`

SIMULATED. Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/code_review/review-request`

SIMULATED. Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/code_review/pr-closed`

SIMULATED. Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/offboarding/repository`

Real. Drops the repository's code index via `codeindex.DeleteRepo` so stale chunks are no longer surfaced in explorer searches. The `provider`, `repository_owner`, and `repository_name` are derived from the request's `repository_name` field (format `"owner/name"`). Always returns `{"success": true}`.

Request example:

```json
{
  "organization_id": 1,
  "repository_id": 123,
  "provider": "github",
  "repository_name": "acme/app"
}
```

Response:

```json
{"success":true}
```

### `POST /v1/pr-metrics/delegated-agent-match`

Real. Looks up the autofix run by provider+PR pair or by PR URL substring in the state blob. On a match, returns HTTP 200 with `run_id`, `agent_id`, `signal_type`, and `match_path`. On no match, returns HTTP 202 (no synchronous match).

Request example:

```json
{"organization_id": 1, "pull_request_id": 456, "provider": "github", "repo": "acme/app"}
```

Response (HTTP 200, match found):

```json
{"run_id":1,"agent_id":"agent-1","signal_type":"seer","match_path":"src/foo.py"}
```

Response (HTTP 202, no match):

```json
{"success":true}
```

### `POST /v1/pr-metrics/pr-close-judge`

SIMULATED. Status-only; the verdict arrives later via Sentry's callback path (`update_pr_metrics`). The consumer checks only the HTTP status code.

Response:

```json
{"success":true}
```

---

## Grouping, supergroups, severity, models, monitoring

### `POST /v0/issues/similar-issues`

Real vector search. `training_mode` causes upsert instead of search.

Request example:

```json
{"project_id": 1, "stacktrace": "Traceback...", "hash": "abc123", "k": 5, "threshold": 0.5, "training_mode": false}
```

Response:

```json
{"responses":[{"parent_hash":"def456","stacktrace_distance":0.2,"should_group":true}],"model_used":"text-embedding-3-small"}
```

### `GET /v0/issues/similar-issues/grouping-record/delete/{project_id}`

Real delete.

Response:

```json
{"success":true}
```

### `POST /v0/issues/similar-issues/grouping-record/delete-by-hash`

Real delete.

Request example:

```json
{"project_id": 1, "hash_list": ["abc123", "def456"]}
```

Response:

```json
{"success":true}
```

### `POST /v0/issues/supergroups/cluster-lightweight`

SIMULATED. Lightweight RCA clustering needs a grouping model, so the payload is acknowledged and no artifact is produced.

Request example:

```json
{"organization_id": 1, "group_id": 1, "issue": {"title": "TypeError"}}
```

Response:

```json
{"success":true}
```

### `POST /v0/issues/supergroups/get`

Real read from the vector store; data is never null.

Request example:

```json
{"organization_id": 1, "supergroup_id": 1}
```

Response:

```json
{"data":[...]}
```

### `POST /v0/issues/supergroups/get-by-group-ids`

Real read.

Request example:

```json
{"organization_id": 1, "group_ids": [1, 2, 3]}
```

Response:

```json
{"data":[...]}
```

### `POST /v0/issues/severity-score`

Real. Calls the LLM for a severity JSON `{"severity": 0.0..1.0}`. Falls back to the deterministic token/heuristic scoring when the LLM fails, so the endpoint never 500s.

Request example:

```json
{"message": "panic: nil pointer dereference", "has_stacktrace": 1, "handled": false}
```

Response:

```json
{"severity":0.75}
```

### `GET /v1/models`

From config. Sentry caches it for 10 minutes.

Response:

```json
{"models":["gpt-4.1-mini"]}
```

### `POST /v1/llm/generate`

Real. Calls the LLM with the request's `system_prompt`, `prompt`, `temperature`, and `max_tokens` and returns `{"content": text, "model": model}`. Consumers parse `content` as JSON and handle failure.

Request example:

```json
{"referrer": "test", "prompt": "Hello"}
```

Response:

```json
{"content":"","model":""}
```

### `POST /v1/monitoring-providers/gcp/verify-connection`

Real when `GCP_SERVICE_ACCOUNT_JSON` is configured. Loads the service account (inline JSON or filesystem path), exchanges a signed JWT for an OAuth2 access token, and checks project access via Cloud Resource Manager and Service Usage APIs. When the env var is unset, returns `"connected"` for every project without performing real verification.

Request example:

```json
{"sentry_sa_email": "sentry@acme.iam.gserviceaccount.com", "customer_sa_email": "dev@acme.iam.gserviceaccount.com", "gcp_project_ids": ["acme-prod"]}
```

Response:

```json
{"connection_status":"connected","projects":[{"gcp_project_id":"acme-prod","connection_status":"connected","services":[],"error_detail":null}],"error_detail":null}
```

---

## Project preference maintenance

### `POST /v1/project-preference/remove-repository`

Real. Removes the matching `{provider, external_id}` from the organization's stored repository preferences and saves. Idempotent: removing a repository twice reports success twice.

Request example:

```json
{"organization_id": 1, "provider": "github", "external_id": "12345"}
```

Response:

```json
{"success":true}
```

### `POST /v1/project-preference/bulk-remove-repositories`

Real. Removes all entries matching the request's `repositories` list and saves.

Request example:

```json
{"organization_id": 1, "repositories": [{"provider": "github", "external_id": "12345"}, {"provider": "github", "external_id": "67890"}]}
```

Response:

```json
{"success":true}
```

### `POST /v1/project-preference/remove-handoffs-for-integration`

Real. Clears the `integration_id` field for the organization and saves.

Request example:

```json
{"organization_id": 1, "integration_id": 42}
```

Response:

```json
{"success":true}
```

---

## Deliberately not served (404)

These paths appear nowhere in Sentry's Seer client layer and return `404`:

- `/v1/automation/autofix/start`
- `/v1/automation/autofix/update`
- `/v1/automation/autofix/state`
- `/v1/automation/autofix/state/pr`
- `/v1/automation/autofix/prompt`
- `/v1/automation/codebase/repo/check-access`
- `/v1/issues/severity-score`
- `/v1/project-preference`
- `/v1/project-preference/set`
- `/v1/project-preference/bulk`
- `/v1/project-preference/bulk-set`
- `POST /v0/issues/similar-issues/grouping-record`
- `POST /v0/issues/supergroups`
- `POST /v0/issues/supergroups/list`
- `POST /v1/automation/explorer/runs`

`internal/handler/routes_test.go` pins both halves of this contract.
