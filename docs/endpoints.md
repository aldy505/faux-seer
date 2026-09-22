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

Acknowledgement stub. faux-seer runs no agent features; Sentry's outbox only requires a non-null `run_id` in the response.

Request example:

```json
{"feature_id": "explorer", "payload": {"query": "hello"}}
```

Response:

```json
{"success":true,"run_id":1}
```

### `POST /v1/automation/explorer/chat`

Real. Persists a run and calls the configured LLM.

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

Real. Returns the stored run state. Sentry builds a pydantic `SeerRunState` requiring `run_id`, `blocks[]`, `status`, and `updated_at`.

Request example:

```json
{"organization_id": 1, "run_id": 1}
```

Response:

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

Stub. faux-seer stores no repository selection.

Request example:

```json
{"run_id": 1, "organization_id": 1}
```

Response:

```json
{"repos":[]}
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

Ack stub. faux-seer keeps no search index; any JSON body is accepted and success is reported immediately.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/index/org-repo-knowledge`

Ack stub.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/index/org-project-knowledge`

Ack stub.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/index/sentry-knowledge`

Ack stub.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/explorer/export-indexes`

Ack stub.

Request example:

```json
{"org_id": 1}
```

Response:

```json
{"success":true}
```

### `POST /v1/explorer/service-map/update`

Status-only consumer.

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

Real. Persists coding-agent state onto an existing run.

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

Unknown `run_id` returns `{"run_id":1,"status":"error","message":"run not found"}` with status `200`.

### `POST /v1/automation/autofix/coding-agent/state/update`

Real. Merges an update into one agent entry.

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

Status-only consumer.

Request example:

```json
{"any_key": "any_value"}
```

Response:

```json
{"success":true}
```

### `POST /v1/automation/oneshot/run`

Stub. Callers extract keys from `result`.

Request example:

```json
{"oneshot_id": 1, "payload": {"type": "test"}}
```

Response:

```json
{"result":{}}
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

Heuristic.

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

Heuristic.

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

Stub. `is_spam` must be a JSON bool.

Request example:

```json
{"organization_id": 1, "feedback_message": "Great app!"}
```

Response:

```json
{"is_spam":false}
```

### `POST /v1/automation/summarize/feedback/labels`

Stub. Nested object required.

Request example:

```json
{"organization_id": 1, "feedback_message": "The login page is broken."}
```

Response:

```json
{"data":{"labels":[]}}
```

### `POST /v1/automation/summarize/feedback/title`

Derived from the message.

Request example:

```json
{"organization_id": 1, "feedback_message": "The login page is broken."}
```

Response:

```json
{"title":"The login page is broken."}
```

### `POST /v1/automation/summarize/feedback/label-groups`

One entry per requested label.

Request example:

```json
{"labels": ["bug", "feature"]}
```

Response:

```json
{"data":[{"primaryLabel":"bug","associatedLabels":[]},{"primaryLabel":"feature","associatedLabels":[]}]}
```

### `POST /v1/automation/summarize/feedback/summarize`

Deterministic.

Request example:

```json
{"feedbacks": [{"message": "App crashes"}, {"message": "Slow loading"}]}
```

Response:

```json
{"data":"Compatibility summary of 2 user feedback items."}
```

### `POST /v1/automation/summarize/replay/breadcrumbs/start`

Frontend-facing shape.

Request example:

```json
{"replay_id": 1, "num_segments": 3}
```

Response:

```json
{"created_at":"2026-05-12T05:39:26Z","status":"completed","num_segments":3,"data":{"summary":"Breadcrumbs summarized.","time_ranges":[]}}
```

### `POST /v1/automation/summarize/replay/breadcrumbs/state`

Polled by the frontend. Status is a lowercase enum: `processing`, `completed`, `error`, `not_started`. Returns the same shape as `start`.

Request example:

```json
{"replay_id": 1, "organization_id": 1, "project_id": 1}
```

Response:

```json
{"created_at":"2026-05-12T05:39:26Z","status":"completed","num_segments":null,"data":{"summary":"Breadcrumbs summarized.","time_ranges":[]}}
```

### `POST /v1/automation/summarize/replay/breadcrumbs/delete`

Status-only consumer.

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

Stub. Sentry validates `runId >= 1`, `created` bool, and `projection` dict.

Request example:

```json
{"requestId": "550e8400-e29b-41d4-a716-446655440000", "investigationId": "inv-1", "source": "seer", "activeTimeBudgetSeconds": 30}
```

Response:

```json
{"runId":1,"created":true,"projection":{}}
```

### `POST /v1/automation/investigations/{run_id}/commands`

Stub. `accepted` must be exactly `true`. `requestId` is echoed when it parses as UUID, else a fresh v4 is generated.

Request example:

```json
{"requestId": "550e8400-e29b-41d4-a716-446655440000", "expectedWorkflowVersion": 1, "command": {"type": "step"}}
```

Response:

```json
{"runId":1,"requestId":"550e8400-e29b-41d4-a716-446655440000","accepted":true,"duplicate":false,"workflowVersion":1,"projection":{}}
```

### `GET /v1/automation/investigations/{run_id}`

Stub.

Response:

```json
{"runId":1,"created":true,"projection":{}}
```

---

## Issue detection

### `POST /v1/automation/issue-detection/analyze`

Returns **202** (not 200). The response body is never parsed by Sentry.

Request example:

```json
{"traces": [{"trace_id": "abc123"}], "organization_id": 1, "project_id": 1, "org_slug": "acme", "plan_tier": "business"}
```

Response (HTTP 202):

```json
{"success":true}
```

### `GET /v1/automation/issue-detection/check-budget/{org_id}?plan_tier=`

Sentry fails open when absent.

Response:

```json
{"has_budget":true}
```

---

## Assisted query

### `POST /v1/assisted-query/start`

Sentry's outbox reads `run_id`.

Response:

```json
{"run_id":1}
```

### `POST /v1/assisted-query/state`

Passthrough to frontend.

Response:

```json
{
  "session": {
    "run_id": 1,
    "status": "completed",
    "current_step": null,
    "completed_steps": [],
    "updated_at": "2026-05-12T05:39:26Z",
    "final_response": null,
    "unsupported_reason": null
  }
}
```

### `POST /v1/assisted-query/translate`

Consumer reads `responses` and `unsupported_reason`.

Response:

```json
{"responses":[],"unsupported_reason":null}
```

### `POST /v1/assisted-query/translate-agentic`

Passthrough to frontend.

Response:

```json
{"responses":[],"unsupported_reason":null}
```

### `POST /v1/assisted-query/create-cache`

Status-only consumer.

Response:

```json
{"success":true}
```

---

## Anomaly detection, breakpoints, workflows

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

Consumer iterates `results`.

Response:

```json
{"results":[]}
```

### `POST /trends/breakpoint-detector`

Empty `data` means no breakpoints detected.

Response:

```json
{"data":[]}
```

---

## Code review, offboarding, PR metrics

### `POST /v1/code_review/check/rerun`

Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/code_review/review-request`

Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/code_review/pr-closed`

Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/offboarding/repository`

Status-only consumer.

Response:

```json
{"success":true}
```

### `POST /v1/pr-metrics/delegated-agent-match`

Returns **202** (not 200). 202 means "no synchronous match"; returning 200 would require real run/agent state.

Response (HTTP 202):

```json
{"success":true}
```

### `POST /v1/pr-metrics/pr-close-judge`

Status-only; the verdict arrives later via callback.

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

Ack stub. No clustering performed.

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

Deterministic heuristic. Severity is clamped to `[0, 1]`.

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

Stub. Consumers parse `content` as JSON and handle failure.

Request example:

```json
{"referrer": "test", "prompt": "Hello"}
```

Response:

```json
{"content":"","model":""}
```

### `POST /v1/monitoring-providers/gcp/verify-connection`

Simulated. No GCP verification is performed.

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

Ack stub. No preferences stored.

Response:

```json
{"success":true}
```

### `POST /v1/project-preference/bulk-remove-repositories`

Ack stub.

Response:

```json
{"success":true}
```

### `POST /v1/project-preference/remove-handoffs-for-integration`

Ack stub.

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
