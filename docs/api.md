# REST API

The contract is `api/api.yml` (OpenAPI 3.1). It is the source of truth: the
server types, the strict handler interface, and the public Go client in
`pkg/http/api/client` are all generated from it by `make generate`. This page
covers the concepts the spec cannot carry.

Every path is under `/v1`. Health and readiness sit outside the version
prefix, because a probe should not have to track an API major.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/evaluations` | Run one evaluation |
| GET | `/v1/evaluations` | List recorded evaluations, newest first |
| GET | `/v1/evaluations/{evaluationId}` | Fetch one evaluation |
| POST | `/v1/evaluations/{evaluationId}/feedback` | Record an expected outcome |
| GET | `/v1/policies` | List loaded policies |
| GET | `/v1/policies/{policyName}/versions/{policyVersion}` | Fetch one policy |
| POST | `/v1/policies/validate` | Compile a candidate policy without installing it |
| GET | `/healthz` | Liveness |
| GET | `/ready` | Readiness, reports a TypeSafe key is set and the database answers |

Metrics are served on a separate listener and are never on the public port.

## Creating an evaluation

```bash
curl -sS http://127.0.0.1:8080/v1/evaluations \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: EXAMPLE-IDEMPOTENCY-KEY-DO-NOT-USE' \
  -d '{
    "policyRef": {"name": "agent-action-firewall", "version": "1.0.0"},
    "state": {"command": "rm -rf ./build"},
    "facts": {
      "target.ownedBySession": true,
      "authorization.explicit": false
    }
  }'
```

Supply exactly one of `policyRef` and `inlinePolicy`. Supplying both, or
neither, is a 400. Inline policies are refused unless
`VIBECHECK_ALLOW_INLINE_POLICIES` is on.

The response carries the decision and everything needed to audit it:

- `outcome` and `matchedRuleId`, the rule that selected it. A null
  `matchedRuleId` means the policy default applied.
- `executionPath`, one of `pre_rule`, `decision_rule`, or `default`. A
  `pre_rule` path means the provider was never called.
- `policyHash`, the SHA-256 of the exact policy semantics that decided it.
- `answers`, every typed answer with its probabilities.
- `resolvedModel`, the versioned model that actually answered, which is not
  the alias the request asked for.
- `usage`, `providerAttempts`, `providerDurationMs`, `totalDurationMs`.
- `id`, stable and durable. The row exists before the response returns.

## Idempotency

Send an `Idempotency-Key` header to make retries safe. Vibecheck canonicalises
the request and stores its hash with the key.

- Same key, same request: the original evaluation is returned. The provider
  is not called again.
- Same key, different request: 409.
- No key: every call is a fresh evaluation.

Keys expire after `VIBECHECK_IDEMPOTENCY_RETENTION`.

## Pagination

`limit` and `offset`, with `hasMore` in the response body. Ordering is newest
first with the evaluation ID as a tiebreaker, so paging through a table that
is being written to does not skip or duplicate rows. An out-of-range `limit`
is a 400 rather than a silent clamp.

Filter with `policyName`, `policyVersion`, `outcome`, and `status`.

## Failure recorded, not lost

A provider failure still produces an evaluation row. `status` reports what
happened and `failureCode` gives the stable class:

`PROVIDER_AUTH_FAILED`, `PROVIDER_RATE_LIMITED`, `PROVIDER_UNAVAILABLE`,
`PROVIDER_TIMEOUT`, `PROVIDER_PROTOCOL_ERROR`, `PROVIDER_REJECTED_REQUEST`,
`INPUT_VALIDATION_FAILED`.

A caller that got an evaluation ID can always fetch the row and see why there
is no outcome.

## Errors

Every error is the same envelope:

```json
{"code": "BAD_REQUEST", "message": "...", "details": {}}
```

`code` is stable and machine-readable. `message` is for humans and never
carries provider text, because a provider error body can echo the caller's
own state back.

| Status | When |
|---|---|
| 400 | Malformed body, bad parameter, client disconnected |
| 401 | Missing or wrong bearer token |
| 404 | No such evaluation or policy |
| 409 | Idempotency key reused with a different request |
| 413 | Body over `VIBECHECK_MAX_REQUEST_BYTES` |
| 422 | Valid JSON that the policy contract rejects |
| 429 | Over the configured rate limit |
| 502 | Provider answered off-contract |
| 503 | Provider unavailable or out of capacity |
| 504 | Provider did not answer in time |

Request bodies reject unknown top-level fields and concatenated JSON values.
A misspelled key fails with `400` instead of being dropped silently.

## Authentication

Set `VIBECHECK_API_TOKEN` and send `Authorization: Bearer <token>`. The
comparison is constant time. An empty token disables the check, which is the
documented trusted-loopback mode and is only appropriate behind a proxy that
does its own authentication.

## Generated Go client

```go
import "github.com/psyb0t/vibecheck/pkg/http/api/client"
```

Generated from the same spec, so it cannot drift from the server.

## Feedback

`POST /v1/evaluations/{id}/feedback` records what a reviewer thinks the
outcome should have been. It never rewrites the decision it labels. The
original evaluation is immutable; feedback is a separate row pointing at it.
