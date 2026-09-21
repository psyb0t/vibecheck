# Changelog

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
