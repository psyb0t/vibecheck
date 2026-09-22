# Vibecheck setup and current boundary

## Current status

The service runs. One registered Servicepack service, `vibecheck-server`,
starts the whole thing: it parses config, compiles the mounted policies,
opens and migrates the database, builds the TypeSafe Jev adapter, then serves
REST and MCP on one public listener with metrics on a separate internal one.

Built and covered by behavioral tests: the policy compiler and evaluator, the
Jev adapter, SQLite and PostgreSQL persistence with reversible migrations,
encrypted input retention, idempotency, the REST API, the MCP server, the
retention cleanup loop, and the production Docker image.

Idempotency replay is built. Re-evaluating retained inputs and calibration
reports are not built.

Do not claim a route, MCP tool, or config value works until you have found
its implementation and the behavioral test that covers it.

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

Running the service from the published image:

```bash
docker run --rm -p 8080:8080 \
  -e VIBECHECK_HTTP_LISTEN_ADDRESS=0.0.0.0:8080 \
  -e VIBECHECK_METRICS_LISTEN_ADDRESS=0.0.0.0:9091 \
  -e VIBECHECK_TYPESAFE_API_KEY=your-key \
  -v "$PWD/examples/agent-action-firewall:/config/policies:ro" \
  -v vibecheck-data:/data \
  psyb0t/vibecheck:latest run
```

The REST API is under `/v1`. The MCP endpoint is `/mcp`, and `/mcp/` reaches
the same handler without a redirect. `/healthz` and `/ready` are unversioned
and unauthenticated. Metrics are only on the internal listener.

Use an immutable `vX.Y.Z` image in any repeatable environment. The release
pipeline publishes both `latest` from `main` and the matching tag from a tagged
release.

## Configuration

`.env.example` documents every setting with its default. The parser and its
validation live in `internal/pkg/config`. Invalid configuration fails the
process at startup rather than at the first request that reads it.

Two invariants worth knowing: `VIBECHECK_STORE_INPUTS` without a
`VIBECHECK_DATA_KEY` is refused, and the metrics listener must not share an
address with the public API.

Never commit a real `.env`, TypeSafe credential, API token, or encryption
key. The opt-in live-provider suite reads its credential from a gitignored
`.env.real`; see `docs/testing.md`.

## Servicepack updates

Vibecheck owns its product code but keeps the framework update boundary:

```bash
make servicepack-update
make servicepack-update-review
make build
make lint
make test
make test-api
make servicepack-update-merge
```

Do not patch framework-owned files to avoid the update workflow. The next
Servicepack update replaces them.
