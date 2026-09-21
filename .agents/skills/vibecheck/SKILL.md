---
name: vibecheck
description: Develop and verify psyb0t/vibecheck, a Go service for typed classification, scoring, and policy decisions backed by TypeSafe Jev. Use when working in the Vibecheck repository on its Servicepack lifecycle, policy engine, provider adapter, persistence, HTTP API, MCP surface, Docker image, or tests. The current release is an early scaffold, so verify implemented behavior before claiming or using an endpoint.
homepage: https://github.com/psyb0t/vibecheck
user-invocable: true
permissions:
  network: "Setup reaches GitHub to clone the repository and Docker Hub to pull published Vibecheck images. Runtime provider and API endpoints are not available in the current scaffold."
  filesystem:
    read:
      - "**/*.go"
      - "go.mod"
      - "go.sum"
      - "Makefile*"
      - "README.md"
      - ".env.example"
      - "docker-compose.yml"
    write:
      - "internal/pkg/services/**"
      - "cmd/init.go"
      - "cmd/commands.go"
      - "tests/**"
      - "docs/**"
  shell:
    - "make generate"
    - "make build"
    - "make lint"
    - "make test"
    - "make test-integration"
    - "make test-coverage"
    - "make sec"
    - "make audit-compose"
    - "make docker-build"
metadata:
  openclaw:
    emoji: "✅"
    requires:
      bins:
        - docker
        - make
---

# Vibecheck

Vibecheck is being built as a generic typed classification, scoring, and
decision service. A caller will submit state and trusted facts, select a
versioned policy, and receive typed Jev answers plus the deterministic outcome
selected by that policy. The caller, not Vibecheck, executes or enforces the
result.

Read [references/setup.md](references/setup.md) before changing the repository.
It records the current implementation boundary and the supported commands.

## Security and safety

- Treat submitted state, policy files, provider responses, and database data
  as untrusted at their boundaries.
- Never put provider keys, API tokens, or real request data in tracked files,
  prompts, examples, test fixtures, or logs.
- Vibecheck returns decisions. Do not add shell execution, arbitrary webhooks,
  or other ambient authority unless the user explicitly changes that product
  boundary.
- Do not hand-edit Servicepack-owned files. Update them with the documented
  Servicepack workflow.

## When to use

- Implementing or reviewing the Vibecheck policy engine, Jev adapter,
  persistence, HTTP API, MCP surface, or audit trail.
- Running the repository's Docker-backed build, generation, lint, test,
  coverage, security, Compose, or image checks.
- Checking whether public docs and integrations match code that has actually
  landed.

## When not to use

- Calling a deployed Vibecheck API. The current release does not expose one.
- Treating a model probability as permission to execute an action.
- Inventing routes, MCP tools, policy fields, or configuration beyond the
  implementation.
- Editing framework-owned files directly.

## Work in the repository

Use the Make targets. They run the pinned Go toolchain and checks in Docker:

```bash
make generate
make build
make lint
make test
make test-integration
make test-coverage
make sec
make audit-compose
make docker-build
```

Application code belongs under `internal/pkg/services/`, with project hooks in
`cmd/init.go`, commands in `cmd/commands.go`, and integration coverage under
`tests/`.

## Framework boundary

Servicepack owns `internal/app/`, `internal/pkg/service-manager/`,
`pkg/runner/`, `cmd/main.go`, `Makefile.servicepack`,
`scripts/make/servicepack/`, `Dockerfile.servicepack*`, and
`servicepack.version`. Use `make servicepack-update`, review and test its update
branch, then merge through `make servicepack-update-merge`.

## Verify claims

Before documenting or using a route, tool, config value, or response field,
find the registered implementation and its behavioral test. The README status
is authoritative while the project remains an early scaffold.
