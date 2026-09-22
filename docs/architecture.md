# Architecture

Vibecheck turns a fuzzy judgement into a typed, auditable decision. A caller
submits state plus trusted facts and names a policy. Vibecheck asks a
constrained model a fixed set of typed questions, applies deterministic rules
to the answers, and returns one outcome with the rule that selected it.

Vibecheck never executes the action it judges. Enforcing the outcome is the
caller's job. That separation is the point: a probabilistic answer must not
become ambient authority.

## Layers

One process, one service, four layers. Dependencies point downward only.

```
      REST (internal/pkg/http/server)   MCP (internal/pkg/mcp)
                        \                 /
                         v               v
                  core (internal/pkg/core/...)
                    /          |          \
                   v           v           v
              policy        provider        db
```

### policy

`internal/pkg/policy` parses and compiles policy documents, then evaluates
compiled rules against facts and answers. It is pure. No HTTP, no database,
no provider, no clock, and no I/O beyond reading a policy directory at
startup.

Compilation resolves every reference once. A rule naming a question that does
not exist, an outcome that is not declared, or a fact that was never required
is a compile error, so it fails at startup rather than on some later request.

Each compiled policy carries a canonical hash over its semantic content.
Reformatting a document leaves the hash alone; changing a rule changes it.
Every evaluation records the hash, so an audit row states which policy
semantics produced the decision.

### provider

`internal/pkg/provider` defines the seam. A `Provider` takes typed questions
and returns typed answers, usage, and per-attempt records. It knows nothing
about HTTP DTOs or database rows.

`internal/pkg/provider/typesafe` adapts the public `pkg/gotypesafe` client to Vibecheck's decision types. The public client is generated from TypeSafe's OpenAPI document and owns authentication, timeouts, concurrency limiting, the response-size ceiling, strict response validation, retry classification, and `Retry-After` handling. The seam lets tests drive the real pipeline with a scripted provider.

### core

`internal/pkg/core` owns orchestration and is the only layer that knows how
the pieces fit together.

- `policies` serves the compiled set and validates candidate documents.
- `evaluations` runs the pipeline: resolve the policy, validate the request
  shape, hash it, check idempotency, snapshot the policy, evaluate pre-rules,
  call the provider, validate answers, evaluate decision rules, persist.

Core also converts database models to the generated API types. Both
transports call the same methods, which is what makes REST and MCP produce
identical decisions and identical audit rows for the same request.

### transports

`internal/pkg/http/server` implements the generated strict-server interface.
A handler decodes, calls exactly one core method, and maps the result. It
holds no business logic and never imports database models or repositories.

`internal/pkg/mcp` serves Streamable HTTP MCP over the same core services.

Both mount on one standard-library HTTP server. Metrics listen on a second,
separate address, so an internal signal is never reachable from the public
port.

## Request path

1. Middleware assigns or sanitises a request ID, then applies the body limit,
   the rate limit, optional bearer auth, and security headers.
2. The handler decodes the request and calls one core method.
3. Core resolves the policy. It accepts exactly one of a named reference or
   an inline document, and inline is off unless explicitly enabled.
4. Core validates the request shape against the configured ceilings, then
   validates the facts against the policy's declared fact contract.
5. Core computes a canonical request hash. With an idempotency key present, a
   matching prior evaluation replays. A reused key carrying a different hash
   is a conflict.
6. Pre-rules run against trusted facts alone. A match settles the decision and
   the provider is never called, so a hard constraint stays outside the
   model's reach and costs no tokens.
7. Otherwise the provider answers the policy's questions. Core validates the
   answers against the question contract. An off-contract answer becomes a
   recorded provider protocol failure rather than a dropped request.
8. Decision rules run in declared order and the first match wins. No match
   means the declared default outcome.
9. Core persists the evaluation before the response returns, so a caller that
   received a response can always find the audit row.

## Persistence

GORM with generated repositories over SQLite or PostgreSQL. Migrations are
reversible, embedded in the binary, and apply the same logical schema to both
engines.

Four tables hold policy snapshots, evaluations, idempotency records, and
feedback. Vibecheck does not store raw caller input unless retention is
switched on, and when it is, it seals the input with AES-256-GCM under an
operator-supplied key.

## Boundaries the build enforces

- Services never import sibling services. Shared vocabulary lives in
  `internal/pkg/common/decision`.
- Handlers never import `db/models` or `db/repositories`.
- `policy` imports nothing from `core`, `http`, `mcp`, or `db`.
- Generated files are regenerated, never hand-edited.

## Related documents

- [Policy authoring](policy-authoring.md)
- [REST API](api.md)
- [MCP](mcp.md)
- [Deployment](deployment.md)
- [Security](security.md)
- [Testing](testing.md)
