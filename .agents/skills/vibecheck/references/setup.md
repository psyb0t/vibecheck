# Run and connect to Vibecheck

Vibecheck ships as `psyb0t/vibecheck` on Docker Hub. Pull and run the image. Do not build from source unless the user explicitly asks for development work.

## Start the image

Create a local policy directory. The repository includes a worked policy that can be downloaded directly:

```bash
mkdir -p policies
curl -fsSL https://raw.githubusercontent.com/psyb0t/vibecheck/main/examples/support-ticket-router/policy.yaml \
  -o policies/support-ticket-router.yaml
```

Start Vibecheck:

```bash
docker run -d --name vibecheck \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e VIBECHECK_HTTP_LISTEN_ADDRESS=0.0.0.0:8080 \
  -e VIBECHECK_METRICS_LISTEN_ADDRESS=0.0.0.0:9091 \
  -e VIBECHECK_TYPESAFE_API_KEY=your-typesafe-api-key-here \
  -v "$PWD/policies:/config/policies:ro" \
  -v vibecheck-data:/data \
  psyb0t/vibecheck:latest run
```

Use `latest` only to try the service. Pin an immutable `vX.Y.Z` image for a lasting deployment. Keep `/data` on a volume and policy files on a read-only mount.

Prove the service is ready:

```bash
curl -fsS http://127.0.0.1:8080/ready
```

## Authentication

Set `VIBECHECK_API_TOKEN` on the container to enable bearer authentication across REST and MCP. Send this header from clients:

```text
Authorization: Bearer <token>
```

An empty token disables application authentication. Keep that mode on loopback or behind an authenticating proxy.

## REST

The REST API is under `/v1`. The main routes are:

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/evaluations` | Evaluate state against a policy |
| GET | `/v1/evaluations` | List durable evaluations |
| GET | `/v1/evaluations/{id}` | Read one evaluation |
| POST | `/v1/evaluations/{id}/feedback` | Record the expected outcome |
| GET | `/v1/policies` | List loaded policies |
| GET | `/v1/policies/{name}/versions/{version}` | Read one policy |
| POST | `/v1/policies/validate` | Compile a candidate without installing it |

Use `Idempotency-Key` on evaluation requests that may be retried. The exact request and response schema lives in `https://github.com/psyb0t/vibecheck/blob/main/api/api.yml`.

## MCP

Use Streamable HTTP at:

```text
http://127.0.0.1:8080/mcp
```

Example client configuration:

```json
{
  "mcpServers": {
    "vibecheck": {
      "type": "http",
      "url": "http://127.0.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer ${VIBECHECK_API_TOKEN}"
      }
    }
  }
}
```

Remove `headers` when authentication is disabled. Both `/mcp` and `/mcp/` work without redirects.

## Policies

Mount YAML or JSON policy files at `/config/policies`. Vibecheck compiles them once during startup and refuses invalid files. Restart the container after changing a mounted policy.

A policy defines trusted fact types, bounded Jev questions, valid outcomes, a default outcome, pre-rules, and ordered decision rules. Read the policy grammar at `https://github.com/psyb0t/vibecheck/blob/main/docs/policy-authoring.md`.

## Storage and retention

SQLite at `/data/vibecheck.sqlite` is the default. Configure PostgreSQL through the `VIBECHECK_DB_*` variables for shared deployments.

Raw state and facts are not retained by default. `VIBECHECK_STORE_INPUTS=true` requires a valid 32-byte base64 `VIBECHECK_DATA_KEY`; startup fails rather than storing plaintext without it. Evaluation retention, idempotency retention, and cleanup intervals are configurable.

## Operations

The public listener provides `/healthz`, `/ready`, REST, and MCP. Prometheus metrics are served at `/metrics` on `VIBECHECK_METRICS_LISTEN_ADDRESS`. Keep the metrics listener private.

Configuration is read once at startup. The full environment reference is `https://github.com/psyb0t/vibecheck/blob/main/.env.example`. Deployment hardening and upgrade instructions are at `https://github.com/psyb0t/vibecheck/blob/main/docs/deployment.md`.
