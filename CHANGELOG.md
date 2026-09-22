# Changelog

## [0.2.1] - 2026-09-22

- Fixed clean-checkout builds by shipping the embedded public suffix data required by the vendored Go dependency graph.
- Clarified that Vibecheck is a general typed classification, scoring, and policy decision service, with the agent action firewall kept as one worked example.

## [0.2.0] - 2026-09-22

- Added the complete typed decision pipeline: versioned YAML policies,
  TypeSafe Jev classification, deterministic outcome rules, and auditable
  evaluation records.
- Added REST and Streamable HTTP MCP surfaces backed by the same core services,
  including policy inspection, validation, evaluation lookup, and feedback.
- Added SQLite and PostgreSQL persistence with reversible migrations,
  idempotent evaluation replay, encrypted optional input retention, and
  configurable cleanup.
- Added optional bearer authentication, strict JSON decoding, request and
  provider size limits, rate limiting, bounded provider concurrency, retry
  policy, request IDs, and redacted structured logs.
- Added separate Prometheus metrics for HTTP, database, provider, policy, MCP,
  and retention operations.
- Added a non-root production image, hardened Compose deployment, generated Go
  client, policy example, operator documentation, and updated agent guidance.
- Added race-tested unit and mock-integration coverage, production-image API
  tests across SQLite and PostgreSQL, security regression tests, and an opt-in
  live TypeSafe contract suite.

## [0.1.2] - 2026-09-21

- Added Docker Hub image publication for `linux/amd64` and `linux/arm64`,
  including immutable release tags, `latest`, SBOMs, provenance, and
  vulnerability reporting.
- Added Claude Code, Codex, and OpenClaw skill packaging with release-time
  ClawHub publication.
- Documented the current image, agent installation commands, and the boundary
  between the scaffold and the planned decision service.
- Fixed the collaborators-only workflow permissions and limited pull request
  checks to non-draft changes.

## [0.1.1] - 2026-09-21

- Updated the Servicepack framework baseline to v1.9.3.
- Replaced timing-sensitive service retry tests with deterministic
  run-triggered cancellation.
- Synced the collaborators-only workflow with the current framework template.

## [0.1.0] - 2026-09-21

Initial Vibecheck scaffold.

- Bootstrapped the Go service with Servicepack and the
  `github.com/psyb0t/vibecheck` module identity.
- Added Docker-backed build, lint, test, coverage, security, and dependency
  workflows.
- Added reusable GitHub Actions for continuous integration, coverage badges,
  security reporting, and tagged releases.
- Added typed environment configuration examples and a hardened production
  Compose baseline.
- Updated the vendored dependency graph to clear reachable security findings
  in the initial Servicepack template.
