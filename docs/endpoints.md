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

Error payload shape:

```json
{"error":"decode severity request: unexpected end of JSON input"}
```

## Health

### `GET /health`
### `GET /health/live`
### `GET /health/ready`

Response example:

```json
{"status":"ok"}
```

Example:

```bash
curl -sS http://127.0.0.1:9091/health
```

## Repo access

### `POST /v1/automation/codebase/repo/check-access`

Request example:

```json
{
  "repo": {
    "provider": "github",
    "owner": "acme",
    "name": "app",
    "external_id": "42"
  }
}
```

Response example:

```json
{"has_access":true}
```

Example:

```bash
BODY='{"repo":{"provider":"github","owner":"acme","name":"app","external_id":"42"}}'
AUTH="Rpcsignature rpc0:$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SEER_SHARED_SECRET" -binary | xxd -p -c 256)"
curl -sS -H "Content-Type: application/json" -H "Authorization: $AUTH" -d "$BODY" \
  http://127.0.0.1:9091/v1/automation/codebase/repo/check-access
```

## Autofix

### `POST /v1/automation/autofix/start`

Request example:

```json
{
  "organization_id": 1,
  "project_id": 2,
  "issue": {
    "id": 123,
    "title": "TypeError in checkout"
  },
  "repos": [
    {
      "provider": "github",
      "owner": "acme",
      "name": "app",
      "external_id": "42"
    }
  ]
}
```

Response example:

```json
{"started":true,"run_id":1}
```

Example:

```bash
BODY='{"organization_id":1,"project_id":2,"issue":{"id":123,"title":"TypeError in checkout"},"repos":[{"provider":"github","owner":"acme","name":"app","external_id":"42"}]}'
AUTH="Rpcsignature rpc0:$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SEER_SHARED_SECRET" -binary | xxd -p -c 256)"
curl -sS -H "Content-Type: application/json" -H "Authorization: $AUTH" -d "$BODY" \
  http://127.0.0.1:9091/v1/automation/autofix/start
```

### `POST /v1/automation/autofix/update`

Request example:

```json
{
  "run_id": 1,
  "payload": {
    "type": "create_pr",
    "repo_external_id": "42",
    "make_pr": true
  }
}
```

Response example:

```json
{"run_id":1,"status":"success"}
```

### `POST /v1/automation/autofix/state`

Request example:

```json
{
  "run_id": 1,
  "group_id": 123,
  "check_repo_access": false,
  "is_user_fetching": true
}
```

Response example:

```json
{
  "group_id": 123,
  "run_id": 1,
  "state": {
    "run_id": 1,
    "status": "COMPLETED",
    "steps": [
      {
        "key": "root_cause_analysis",
        "status": "COMPLETED"
      }
    ],
    "codebases": {
      "42": {
        "repo_external_id": "42",
        "file_changes": [],
        "is_readable": true,
        "is_writeable": true
      }
    },
    "request": {
      "organization_id": 1,
      "project_id": 2
    }
  }
}
```

### `POST /v1/automation/autofix/state/pr`

Request example:

```json
{
  "provider": "42",
  "pr_id": 1711898354
}
```

Response example:

```json
{
  "group_id": 123,
  "run_id": 1,
  "state": {
    "status": "COMPLETED"
  }
}
```

### `POST /v1/automation/autofix/prompt`

Request example:

```json
{
  "run_id": 1,
  "include_root_cause": true,
  "include_solution": true
}
```

Response example:

```json
{
  "prompt": "Please fix the following issue. Ensure that your fix is fully working.\n\nIssue: TypeError in checkout\n\nRepositories: acme/app\n\nRoot cause: Compatibility mode generated a placeholder root-cause analysis from the issue payload.\n\nSolution: Inspect the failing path, implement the smallest safe fix, and add or update tests if needed."
}
```

### `POST /v1/automation/autofix/coding-agent/state/set`

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

Response example:

```json
{"run_id":1,"status":"success"}
```

### `POST /v1/automation/autofix/coding-agent/state/update`

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

Response example:

```json
{"run_id":1,"status":"success"}
```

## Summaries

### `POST /v1/automation/summarize/issue`

Request example:

```json
{
  "group_id": "123",
  "issue": {
    "title": "TypeError in checkout"
  },
  "trace_tree": {
    "id": "trace-1"
  }
}
```

Response example:

```json
{
  "group_id": "123",
  "headline": "TypeError in checkout",
  "whats_wrong": "Stub provider response:\n\nSummarize this issue for an engineer.",
  "trace": "Trace context is available and should be reviewed alongside the issue.",
  "possible_cause": "Inspect the top in-app frames and recent deploys affecting the failing code path.",
  "scores": {
    "possible_cause_confidence": 0.42,
    "possible_cause_novelty": 0.31,
    "fixability_score": 0.58,
    "fixability_score_version": 1,
    "is_fixable": true
  }
}
```

### `POST /v1/automation/summarize/trace`

Request example:

```json
{
  "trace_id": "abc123"
}
```

Response example:

```json
{
  "trace_id": "abc123",
  "summary": "Trace contains a slow or failing execution path that should be inspected span-by-span.",
  "key_observations": "Review the longest-running spans, error spans, and service boundaries.",
  "performance_characteristics": "Look for concentrated latency in the critical path and repeated downstream calls.",
  "suggested_investigations": [
    {
      "explanation": "Inspect the slowest transaction span and its child spans.",
      "span_id": "compat-span-1",
      "span_op": "http.server"
    }
  ]
}
```

### `POST /v1/automation/summarize/fixability`

Request example:

```json
{
  "group_id": "123"
}
```

Response example:

```json
{
  "group_id": "123",
  "headline": "Fixability assessment",
  "whats_wrong": "Compatibility mode returned a heuristic fixability score.",
  "trace": "Detailed trace analysis is not available in heuristic mode.",
  "possible_cause": "This issue appears actionable if you can reproduce it locally or from stacktrace context.",
  "scores": {
    "fixability_score": 0.61,
    "fixability_score_version": 1,
    "is_fixable": true
  }
}
```

## Project preferences

### `POST /v1/project-preference`

Request example:

```json
{"project_id":2}
```

Response example:

```json
{
  "preference": {
    "organization_id": 1,
    "project_id": 2,
    "repositories": []
  }
}
```

### `POST /v1/project-preference/set`

Request example:

```json
{
  "preference": {
    "organization_id": 1,
    "project_id": 2,
    "repositories": [
      {
        "provider": "github",
        "owner": "acme",
        "name": "app",
        "external_id": "42"
      }
    ]
  }
}
```

Response example:

```json
{
  "preference": {
    "organization_id": 1,
    "project_id": 2,
    "repositories": [
      {
        "provider": "github",
        "owner": "acme",
        "name": "app",
        "external_id": "42"
      }
    ]
  }
}
```

### `POST /v1/project-preference/bulk`

Request example:

```json
{"project_ids":[2,3]}
```

Response example:

```json
{
  "preferences": [
    {
      "organization_id": 1,
      "project_id": 2,
      "repositories": []
    }
  ]
}
```

### `POST /v1/project-preference/bulk-set`

Request example:

```json
{
  "preferences": [
    {
      "organization_id": 1,
      "project_id": 2,
      "repositories": []
    },
    {
      "organization_id": 1,
      "project_id": 3,
      "repositories": []
    }
  ]
}
```

Response example:

```json
{
  "preferences": [
    {
      "organization_id": 1,
      "project_id": 2,
      "repositories": []
    },
    {
      "organization_id": 1,
      "project_id": 3,
      "repositories": []
    }
  ]
}
```

### `POST /v1/project-preference/remove-repository`

Request example:

```json
{
  "organization_id": 1,
  "repo_provider": "github",
  "repo_external_id": "42"
}
```

Response example:

```json
{"success":true}
```

## Similarity and grouping

### `POST /v0/issues/similar-issues`

Request example:

```json
{
  "project_id": 1,
  "stacktrace": "TypeError: undefined is not a function",
  "hash": "grouping-hash",
  "k": 3,
  "threshold": 0.1
}
```

Response example:

```json
{
  "responses": [
    {
      "parent_hash": "existing-hash",
      "stacktrace_distance": 0.04,
      "should_group": true
    }
  ],
  "model_used": "text-embedding-3-small"
}
```

### `POST /v0/issues/similar-issues/grouping-record`

Request example:

```json
{
  "data": [
    {
      "group_id": 1,
      "hash": "existing-hash",
      "project_id": 1,
      "exception_type": "TypeError"
    }
  ],
  "stacktrace_list": [
    "TypeError: undefined is not a function"
  ],
  "k": 1,
  "threshold": 0.1
}
```

Response example:

```json
{
  "success": true,
  "groups_with_neighbor": {}
}
```

### `GET /v0/issues/similar-issues/grouping-record/delete/{project_id}`

Path parameters:

- `project_id`: numeric Sentry project ID

Response example:

```json
{"success":true}
```

Example:

```bash
AUTH="Rpcsignature rpc0:$(printf '' | openssl dgst -sha256 -hmac "$SEER_SHARED_SECRET" -binary | xxd -p -c 256)"
curl -sS -H "Authorization: $AUTH" \
  http://127.0.0.1:9091/v0/issues/similar-issues/grouping-record/delete/1
```

### `POST /v0/issues/similar-issues/grouping-record/delete-by-hash`

Request example:

```json
{
  "project_id": 1,
  "hash_list": ["existing-hash"]
}
```

Response example:

```json
{"success":true}
```

## Supergroups

### `POST /v0/issues/supergroups`

Request example:

```json
{
  "organization_id": 1,
  "group_id": 10,
  "project_id": 2,
  "artifact_data": {
    "kind": "compat-supergroup",
    "title": "Checkout failures"
  }
}
```

Response example:

```json
{"success":true}
```

### `POST /v0/issues/supergroups/list`
### `POST /v0/issues/supergroups/get`
### `POST /v0/issues/supergroups/get-by-group-ids`

All three paths currently share the same list behavior in faux-seer.

Request example:

```json
{
  "organization_id": 1,
  "project_ids": [2],
  "offset": 0,
  "limit": 50
}
```

Response example:

```json
{
  "data": [
    {
      "kind": "compat-supergroup",
      "title": "Checkout failures"
    }
  ]
}
```

## Severity

### `POST /v0/issues/severity-score`
### `POST /v1/issues/severity-score`

Request example:

```json
{
  "message": "panic: nil pointer dereference",
  "has_stacktrace": 1,
  "handled": false
}
```

Response example:

```json
{"severity":0.73}
```

Behavior notes:

- faux-seer currently computes a deterministic heuristic score
- scores are clamped to `[0, 1]`
- the same implementation serves both `/v0` and `/v1`
