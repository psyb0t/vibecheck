# Deployment

One container, one policy directory, one TypeSafe key. That is the supported path for a self-hosted deployment. Vibecheck ships as `psyb0t/vibecheck` on Docker Hub.

## Quick start

```bash
mkdir -p policies
curl -fsSL https://raw.githubusercontent.com/psyb0t/vibecheck/main/examples/agent-action-firewall/policy.yaml \
  -o policies/agent-action-firewall.yaml

docker run -d --name vibecheck \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e VIBECHECK_HTTP_LISTEN_ADDRESS=0.0.0.0:8080 \
  -e VIBECHECK_METRICS_LISTEN_ADDRESS=0.0.0.0:9091 \
  -e VIBECHECK_TYPESAFE_API_KEY=your-typesafe-api-key-here \
  -v "$PWD/policies:/config/policies:ro" \
  -v vibecheck-data:/data \
  psyb0t/vibecheck:latest run

curl -fsS http://127.0.0.1:8080/ready
```

The policy directory is mounted read-only at `/config/policies`. Replace the worked example with policies for your own decisions.

Evaluate through REST as shown in [the API doc](api.md), or point an MCP
client at `/mcp` as shown in [the MCP doc](mcp.md).

## The image

Multi-stage build, static binary, non-root `appuser`, and no compiler,
package manager, or source tree in the final layer. `/data` is the only
writable path, which is what lets the container run with a read-only root
filesystem.

`HEALTHCHECK` hits `/ready` rather than `/healthz`. Readiness reports that
the database answers, so a `depends_on: condition: service_healthy` elsewhere
means the container can actually serve rather than merely that the process
exists.

The entrypoint is exec form, so `SIGTERM` reaches the binary and the graceful
drain runs instead of `docker stop` waiting out its timeout.

## Hardened Compose template

The repository's `docker-compose.yml` is a hardened template for operators who want Compose. It uses `cap_drop: [ALL]`, `no-new-privileges`, a read-only root with a size-capped `noexec,nosuid` tmpfs, `init: true`, 512 MiB, 1 CPU, 256 PIDs, bounded json-file logs, an explicit restart policy, and loopback-only published ports. Change its image stanza from the local build to a pinned `psyb0t/vibecheck:vX.Y.Z` image when using it outside a source checkout.

`make audit-compose` checks the file against that floor and fails on banned
settings, unintended public ports, missing limits, or missing log bounds. Run
it after any compose change.

## Exposure

The published port binds `127.0.0.1`. Put a reverse proxy or a tailnet edge
in front of it before anything off-host can reach it.

The metrics listener is a separate address and is never published. It carries
no authentication by design, which is exactly why it must not be reachable
from outside the compose network.

## Database

SQLite is the default and is right for one self-hosted process. The file
lives on the `vibecheck-data` named volume.

PostgreSQL is for shared or replicated deployments. Set
`VIBECHECK_DB_DRIVER=postgres` and the `VIBECHECK_DB_*` connection values.

Migrations are embedded in the binary and run at startup. They are reversible
and apply the same logical schema to both engines. Restarting an existing
deployment re-runs no migration and loses no data; the test suite asserts
both.

## Retention

Vibecheck does not store raw caller state or facts. `VIBECHECK_STORE_INPUTS`
turns retention on, and enabling it without a valid 32-byte base64
`VIBECHECK_DATA_KEY` is refused at startup rather than silently writing
plaintext.

A cleanup loop sweeps expired rows every
`VIBECHECK_RETENTION_CLEANUP_INTERVAL`. `VIBECHECK_EVALUATION_RETENTION=0`
keeps evaluations indefinitely.

## Configuration

`.env.example` documents every variable with its default. Configuration is
read once at startup and never reloaded, so a changed value takes effect on
the next restart. An invalid value fails the process before it serves a
request rather than surfacing on the first call that happens to read it.

The values worth deciding before the first start:

| Variable | Why it matters |
|---|---|
| `VIBECHECK_TYPESAFE_API_KEY` | Required. No key, no decisions. |
| `VIBECHECK_POLICY_DIR` | A missing configured directory fails startup. |
| `VIBECHECK_API_TOKEN` | Empty means no authentication. Only safe behind a proxy that authenticates. |
| `VIBECHECK_ALLOW_INLINE_POLICIES` | Off by default. On widens the surface from "pick a loaded policy" to "supply arbitrary rules". |
| `VIBECHECK_STORE_INPUTS` | Off by default. On needs `VIBECHECK_DATA_KEY`. |
| `VIBECHECK_DB_DRIVER` | `sqlite` for one process, `postgres` for shared. |

## Shutdown

`SIGTERM` starts a graceful drain bounded by `RUNNER_SHUTDOWNTIMEOUT`.
In-flight evaluations are cancelled rather than left to finish against a
provider nobody is waiting on, and the retention loop stops with the process.

## Upgrading

Tagged releases publish immutable images such as `psyb0t/vibecheck:v0.3.0`.
Pin one. `latest` is for trying it out, not for a deployment you intend to
keep.

Migrations run on start, so an upgrade is a pull and a restart. Take a
database backup first; the down migrations exist but a restore is faster.
