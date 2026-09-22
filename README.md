# vibecheck

[![CI](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/coverage.svg)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/version.svg)](https://github.com/psyb0t/vibecheck/releases)
[![license](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/license.svg)](LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/psyb0t/vibecheck?style=flat-square)](https://hub.docker.com/r/psyb0t/vibecheck)

AI agents are brilliant right up until they confidently do stupid shit.
Vibecheck is the bouncer.

Feed it messy state and trusted facts. It asks TypeSafe Jev narrow typed
questions, runs the probabilities through a versioned YAML policy, and returns
the exact rule and outcome that won. No word soup. No mystery branch. No model
with a shell deciding that `0.73` feels close enough.

Vibecheck never executes the action it judges. Your code stays in charge and
decides what `allow`, `review`, `deny`, or your own outcomes actually mean.
Every evaluation gets a stable ID, an audit row, the typed answers and
probabilities, model identity, token usage, and the deterministic rule that
made the call.

It runs today over REST and MCP, backed by SQLite or PostgreSQL, with
Prometheus metrics on a separate internal listener. Idempotent retries replay
the first decision instead of billing the provider twice. Re-evaluating stored
inputs and calibration reports are not built yet.

## Make it judge something

Mount a policy, give it a TypeSafe key, and run the bastard:

```bash
docker run --rm -p 8080:8080 \
  -e VIBECHECK_HTTP_LISTEN_ADDRESS=0.0.0.0:8080 \
  -e VIBECHECK_METRICS_LISTEN_ADDRESS=0.0.0.0:9091 \
  -e VIBECHECK_TYPESAFE_API_KEY=your-key \
  -v "$PWD/examples/agent-action-firewall:/config/policies:ro" \
  -v vibecheck-data:/data \
  psyb0t/vibecheck:latest run
```

The public listener serves REST under `/v1`, Streamable HTTP MCP at `/mcp`,
and the unversioned `/healthz` and `/ready` probes. Prometheus metrics live at
`/metrics` on the separate internal listener, where random callers cannot use
traffic shape and error rates as free reconnaissance.

Both `/mcp` and `/mcp/` hit the handler directly. No redirect eats your POST
body. Policies are compiled once at startup, so a half-written YAML file never
becomes a live rule set. Restart to load a policy change.

For an actual deployment, use the hardened Compose file:

```bash
cp .env.example .env
docker compose up -d
```

It runs non-root with every capability dropped, a read-only filesystem, and
bounded CPU, memory, PIDs, and logs. Ports bind to loopback. Read
[deployment](docs/deployment.md) before putting it anywhere hostile.

## Hack on it

Every operation runs through Docker, so a clean machine needs Docker, Make,
Git, and a shell. There is no host Go toolchain requirement.

```bash
make help              # list every target
make generate          # OpenAPI server + client, typed constants, repositories
make build             # static binary in build/
make lint              # shell and Go linting
make test              # race-enabled test suite
make test-coverage     # coverage gate
make sec               # govulncheck + semgrep
make audit-compose     # check docker-compose.yml against the hardening floor
```

`make generate` runs three generators: `oapi-codegen` over `api/api.yml` for
the server types and the public client, `oapixconstgen` for the constants
shared between the contract and the Go code, and `gorm-gen` over the models
for the typed repositories. Generated files carry a generated marker and are
regenerated rather than edited.

`make test` covers the unit and mock-integration layers. `make test-api`
builds the production image and drives it over real HTTP with testcontainers,
against both SQLite and PostgreSQL. `make test-real` is opt-in and bills a
real TypeSafe account. See [docs/testing.md](docs/testing.md).

`.env.example` documents every setting with its default. Real `.env` files are
gitignored.

Tagged releases publish matching immutable images such as
`psyb0t/vibecheck:v0.2.0`. Pin one for anything you intend to keep. `latest`
is for kicking the tires.

## Give agents the manual

The bundled skill tells Claude Code, Codex, and OpenClaw how this repository
actually works, including the Servicepack boundary and Docker-backed checks.

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

ClawHub publication happens from release tags.

A running Vibecheck is also an MCP server. Point any MCP client at
`http://<host>:8080/mcp` over Streamable HTTP. It exposes six tools:
`vibecheck_evaluate`, `vibecheck_list_policies`, `vibecheck_get_policy`,
`vibecheck_get_evaluation`, `vibecheck_validate_policy`, and
`vibecheck_submit_feedback`. They call the same services the REST API calls,
so a decision made over MCP produces the same audit row.

## Where the bodies are buried

This project is built on [servicepack](https://github.com/psyb0t/servicepack)
and keeps its ownership boundary. `internal/app/`,
`internal/pkg/service-manager/`, `pkg/runner/`, `cmd/main.go`,
`Makefile.servicepack`, `scripts/make/servicepack/`, `Dockerfile.servicepack*`,
and `servicepack.version` belong to the framework and are replaced by
`make servicepack-update`. Application code lives in `internal/pkg/services/`,
with project extension points in `cmd/init.go`, `cmd/commands.go`, `Makefile`,
and `scripts/make/`.

## Read the rest

- [Architecture](docs/architecture.md), the layers and the request path
- [Policy authoring](docs/policy-authoring.md), the document grammar
- [REST API](docs/api.md), concepts behind `api/api.yml`
- [MCP](docs/mcp.md), the endpoint and the six tools
- [Deployment](docs/deployment.md), the image, Compose, and configuration
- [Security](docs/security.md), the threat model and the bounds
- [Testing](docs/testing.md), the four test layers
- [Example walkthrough](docs/agent-action-firewall.md)

## License

MIT. See [LICENSE](LICENSE). Release history is in [CHANGELOG.md](CHANGELOG.md).

*Built with spite using https://github.com/psyb0t/servicepack*
