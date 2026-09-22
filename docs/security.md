# Security

## The threat this service exists to address

An agent that can both judge an action and perform it has no boundary. The
judgement is probabilistic, the action is real, and the only thing between
them is the model's own agreement.

Vibecheck splits those apart. It returns an outcome and never executes
anything. The caller enforces the answer. That means a compromised or
confused model can, at worst, return a wrong outcome, and it can never turn
that outcome into an action on its own.

Callers should fail closed. If Vibecheck is unreachable, treat a destructive
action as `review` or `block`, never as `allow`.

## Pre-rules are the boundary, decision rules are the judgement

Pre-rules run against trusted facts before the provider is called. Nothing
the caller puts in `state` can influence them, because they never read
`state`.

Put every hard constraint in `preRules`. Ownership, authorization, and
sandbox boundaries belong there. A rule that only appears in
`decisionRules` depends on a model answer and is therefore a suggestion, not
a boundary.

The shipped example's acceptance vectors include adversarial `state` text
that instructs the model to approve a destructive action on an unowned
target. The pre-rule blocks it and the provider is never called, so the
instruction is never read.

## Prompt injection

State is caller-supplied and untrusted. Treat it that way:

- Facts, not state, drive pre-rules.
- Questions are fixed by the policy. A caller cannot add, remove, or reword
  one through the request.
- Answers are validated against the question contract. A choice outside the
  declared set, a score outside the criteria range, or a probability
  distribution that does not sum to one is a protocol failure, not an
  answer.
- Outcomes are a closed set declared by the policy. A model cannot invent
  one.

## Provider output never reaches the caller

A provider error body can echo the request back. Vibecheck never forwards
provider text into an API response or a log line. Errors are mapped to
stable codes and fixed messages.

An integration test plants a marker string in the fake provider's error
bodies and asserts it never appears in any Vibecheck response or log record.

## Input bounds

Every caller-supplied dimension has a ceiling, enforced before anything
expensive happens:

| Limit | Variable |
|---|---|
| Request body | `VIBECHECK_MAX_REQUEST_BYTES` |
| State payload | `VIBECHECK_MAX_STATE_BYTES` |
| Fact count | `VIBECHECK_MAX_FACTS` |
| Question count | `VIBECHECK_MAX_QUESTIONS` |
| Inline policy size | `VIBECHECK_MAX_POLICY_BYTES` |
| Metadata entries and value length | `VIBECHECK_MAX_METADATA_ENTRIES`, `VIBECHECK_MAX_METADATA_VALUE_LENGTH` |

The body limit uses `http.MaxBytesReader`, so an oversized request costs the
ceiling and not one byte more.

Policy documents and REST request bodies use unknown-field checking. A typo in
a mounted policy fails startup, and a misspelled top-level request field fails
with `400` instead of being dropped silently.

Policy loading is bounded too. A policy directory is read once at startup,
symlinks and non-regular files are refused, and an oversized document fails
rather than being truncated.

## Authentication and rate limiting

`VIBECHECK_API_TOKEN` enables bearer auth across both REST and MCP. The
comparison is constant time, so a wrong token cannot be recovered by timing
the response.

An empty token disables the check. That is the trusted-loopback mode and is
only appropriate when something in front of Vibecheck authenticates.

The rate limiter is one global token bucket covering REST and MCP. It is
deliberately not per-client: Vibecheck sits behind a proxy or on loopback, so
the remote address is usually the proxy, and a per-address bucket would
either be one bucket anyway or be evaded by a forged forwarding header.

## Secrets and logging

Logs are structured and carry no credentials, tokens, request bodies, or
arbitrary headers. Request IDs are shape-validated and length-bounded before
they reach a log line, so a caller cannot inject arbitrary text into every
record for a request.

The TypeSafe key is read from the environment and never logged, never echoed,
and never written to the database.

## Data at rest

Raw state and facts are not stored unless `VIBECHECK_STORE_INPUTS` is on.
When it is on, the payload is sealed with AES-256-GCM under
`VIBECHECK_DATA_KEY`. Enabling retention without a valid 32-byte base64 key
fails at startup instead of writing plaintext.

Evaluation rows always keep the decision, the answers, and the policy hash.
Those are the audit trail and they contain no caller payload.

## Container posture

Non-root user, read-only root filesystem, all capabilities dropped,
`no-new-privileges`, and memory, CPU, and PID ceilings. `make audit-compose`
enforces that floor. See [deployment](deployment.md).

The metrics listener has no authentication and must never be published. It is
a separate address for exactly that reason.

## Gates

`make sec` runs govulncheck and semgrep and writes `sec.sarif`. `make lint`
runs the full golangci-lint set, including `gosec`. Both are release gates.

## Reporting

Open a security issue at
<https://github.com/psyb0t/vibecheck/security/advisories>.
