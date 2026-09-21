# vibecheck

[![CI](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/coverage.svg)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/version.svg)](https://github.com/psyb0t/vibecheck/releases)
[![license](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/license.svg)](LICENSE)

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
