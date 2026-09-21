# vibecheck

[![CI](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/coverage.svg)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/version.svg)](https://github.com/psyb0t/vibecheck/releases)
[![license](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/license.svg)](LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/psyb0t/vibecheck?style=flat-square)](https://hub.docker.com/r/psyb0t/vibecheck)

Typed classification, scoring, and decision service built on TypeSafe Jev.

A caller submits state plus trusted facts, selects a versioned policy, and gets
back the selected outcome, the deterministic rule that selected it, every typed
Jev answer with its probabilities, model and token usage, and a stable
evaluation ID. Vibecheck never executes the action it judges; enforcing the
returned outcome stays with the caller.

> **Status: early.** The service skeleton is in place. The policy compiler,
> provider adapter, persistence, HTTP API, and MCP surface are not implemented
> yet, so there is nothing to deploy.

## Development

Every operation runs through Docker, so a clean machine needs Docker, Make,
Git, and a shell. There is no host Go toolchain requirement.

```bash
make help              # list every target
make build             # static binary in build/
make lint              # shell and Go linting
make test              # race-enabled test suite
make test-coverage     # coverage gate
make sec               # govulncheck + semgrep
make audit-compose     # check docker-compose.yml against the hardening floor
```

`.env.example` records the planned service configuration. Those values become
active as the corresponding implementation phases land. Real `.env` files are
gitignored.

The current image exposes the scaffold CLI while the service is being built:

```bash
docker pull psyb0t/vibecheck:latest
docker run --rm psyb0t/vibecheck:latest --help
```

Tagged releases publish matching immutable images such as
`psyb0t/vibecheck:v0.1.2`. Do not deploy the image as a decision service until
the status notice above says the API is implemented.

## Agent integrations

The bundled skill gives Claude Code, Codex, and OpenClaw the verified project
boundaries and Docker-backed development commands. It does not invent an API
or MCP surface that has not landed yet.

```bash
# Claude Code
claude plugin marketplace add psyb0t/agents
claude plugin install vibecheck@psyb0t

# Codex
codex plugin marketplace add psyb0t/agents
codex plugin add vibecheck@psyb0t

# OpenClaw
openclaw skills install @psyb0t/vibecheck
```

ClawHub publication happens from release tags. There is no OpenClaw MCP plugin
or MCP registry entry until Vibecheck actually exposes an MCP server.

## Layout

This project is built on [servicepack](https://github.com/psyb0t/servicepack)
and keeps its ownership boundary. `internal/app/`,
`internal/pkg/service-manager/`, `pkg/runner/`, `cmd/main.go`,
`Makefile.servicepack`, `scripts/make/servicepack/`, `Dockerfile.servicepack*`,
and `servicepack.version` belong to the framework and are replaced by
`make servicepack-update`. Application code lives in `internal/pkg/services/`,
with project extension points in `cmd/init.go`, `cmd/commands.go`, `Makefile`,
and `scripts/make/`.

## License

MIT. See [LICENSE](LICENSE). Release history is in
[CHANGELOG.md](CHANGELOG.md).

---

*Built with spite using https://github.com/psyb0t/servicepack*
