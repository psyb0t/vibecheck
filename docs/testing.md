# Testing

Vibecheck is tested in four layers. Each one catches a class of bug the layer
below it cannot.

Everything runs in Docker. The host needs Docker, Make, Git, and a shell. No
target installs a Go toolchain on the host.

| Layer | Command | What it proves |
|---|---|---|
| Unit | `make test` | Boundaries and security decisions in isolation |
| Mock integration | `make test` | The pipeline against a scripted provider |
| Real API | `make test-api` | The production image, over real HTTP |
| Live provider | `make test-real` | The upstream contract still matches the code |

## Unit

Test packages sit next to the code they cover and run under `-race`. They
cover the places where a wrong answer is a security problem rather than a
bug: configuration validation, bearer-token comparison, request-ID
sanitisation, error classification, and metric label cardinality.

These tests assert on behavior, never on source text. A test that greps a
file for a function name proves the file has not changed. It proves nothing
about what the code does.

## Mock integration

`internal/pkg/core/evaluations` drives the real pipeline against a scripted
provider and a real SQLite database: policy compilation, pre-rules, the
provider call, answer validation, decision rules, and persistence. It calls
the same exported methods both transports call.

## Real API

`make test-api` builds the production image from the repo `Dockerfile`,
starts it with testcontainers, and talks to it over real HTTP. Nothing inside
the process is stubbed. If the image ships a root-owned binary, a missing
writable path, a broken entrypoint, or a listener that never binds, these
tests fail.

What it covers:

- REST: every endpoint, validation failures, the stable `{code, message,
  details}` error envelope, multi-page pagination with no gaps or duplicates,
  filtering, and feedback that never rewrites the decision it labels.
- MCP: `initialize`, `tools/list`, and every tool. Both `/mcp` and `/mcp/`
  must reach the handler with no redirect, because a redirected POST loses
  its body.
- Cross-transport equivalence: an evaluation created over MCP is the same
  audit row REST serves.
- Persistence: SQLite and PostgreSQL, both with real migrations. Migrations
  run twice against one database to prove a restart is a no-op.
- Idempotency: a replay returns the original evaluation and does not bill a
  second provider call. Reusing a key with different content is a 409.
- Provider behavior: transient failures retry, authentication failures do
  not, an exhausted budget still records an audit row, and an unusable
  response is a 502 rather than a 500.
- Security: bearer auth on both transports, oversized bodies, malformed
  input, client cancellation, encrypted retained input, and the guarantee
  that no credential, request body, or upstream provider message reaches a
  log line or a caller.
- Retention: the cleanup loop deletes expired rows, and leaves them alone
  when the window is zero.

The provider is a local fake, `tests/testinfra/fakeprovider.go`, speaking the
documented System One wire contract over real HTTP. It is a fake, not a mock.
The service reaches it through its real adapter, so serialization, status
handling, retry, and cancellation all run for real. Tests script its replies
to drive paths a live provider cannot be asked for on demand, such as a 529
followed by a success.

### Why this layer needs the Docker socket

`make test-api` runs under `DEV_RUN_DIND`, which mounts the Docker socket so
the test process can start sibling containers. That is the only category of
target allowed to do it. Lint, unit tests, generation, and build never get
the socket. Neither the production image nor the compose file inherits this
exception.

## Live provider

`make test-real` is opt-in and bills a real TypeSafe account. It is not part
of `make test` and never runs in CI.

It exists because the local fake is faithful to the contract as documented.
If TypeSafe changes the System One response shape, every other layer stays
green while production breaks. Run this when changing the provider
integration or before a release, not on every commit.

Setup:

```bash
echo 'VIBECHECK_TYPESAFE_API_KEY=EXAMPLE-DO-NOT-USE' > .env.real
make test-real
```

`.env.real` is gitignored. The credential reaches the container through a
`--env-file` scoped to that one target, so no other Make target can pull it
into its environment. Without the file the target refuses instead of quietly
skipping.

The suite asserts on contract shape, not on a specific decision. A real model
may disagree about how risky a sentence is. It may not answer in a shape the
adapter cannot decode.

## Coverage

`make test-coverage` runs the unit and integration suites together with
`-tags=integration` and enforces the repository threshold. It is the only
framework target that sets that build tag, which is why `make test-api`
exists as a way to run the API suite on its own while iterating.
