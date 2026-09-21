# Vibecheck setup and current boundary

## Current status

The repository currently contains the Servicepack scaffold, Docker-backed
tooling, a hardened future deployment baseline, and a placeholder hello-world
service. The policy compiler, TypeSafe Jev adapter, persistence, HTTP API, and
MCP server are not implemented yet.

Do not claim that `docker compose up`, an HTTP route, or an MCP tool works until
the corresponding code and behavioral tests land.

## Development setup

Requirements:

- Docker with Compose support
- Make
- Git

The host does not need Go. Build and checks use the pinned development image.

```bash
git clone https://github.com/psyb0t/vibecheck
cd vibecheck
make help
make build
make test
```

The production image can currently prove the scaffold CLI only:

```bash
docker pull psyb0t/vibecheck:latest
docker run --rm psyb0t/vibecheck:latest --help
```

Use an immutable `vX.Y.Z` image in any repeatable environment. The release
pipeline publishes both `latest` from `main` and the matching tag from a tagged
release.

## Configuration

`.env.example` records the planned configuration contract. Values are not
active merely because they appear there. Confirm each value against its config
struct and startup validation before relying on it.

Never commit a real `.env`, TypeSafe credential, API token, or encryption key.

## Servicepack updates

Vibecheck owns its product code but keeps the framework update boundary:

```bash
make servicepack-update
make servicepack-update-review
make build
make lint
make test
make servicepack-update-merge
```

Do not patch framework-owned files to avoid the update workflow. The next
Servicepack update replaces them.
