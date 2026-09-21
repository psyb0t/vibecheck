# Changelog

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
