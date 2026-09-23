# vibecheck

[![CI](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml/badge.svg?branch=main)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![coverage](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/coverage.svg)](https://github.com/psyb0t/vibecheck/actions/workflows/pipeline.yml)
[![version](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/version.svg)](https://github.com/psyb0t/vibecheck/releases)
[![license](https://raw.githubusercontent.com/psyb0t/vibecheck/badges/license.svg)](LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/psyb0t/vibecheck?style=flat-square)](https://hub.docker.com/r/psyb0t/vibecheck)

Most software has to make fuzzy calls from messy state. Vibecheck turns those calls into typed answers and deterministic policy outcomes.

Send it unstructured state, trusted facts, and a versioned policy. It asks TypeSafe Jev bounded questions, applies your YAML rules, and returns the outcome, probabilities, matched rule, model identity, timing, and a stable evaluation ID. Vibecheck can classify, score, route, approve, review, or block anything whose valid outcomes are known in advance.

Vibecheck ships as a Docker image. You run the image, mount policies, then call it over REST or Streamable HTTP MCP. It never executes the action it judges. Your caller decides what each outcome means.

## Contents

| Section | What it covers |
| --- | --- |
| [Quick start](#quick-start) | Route one support message with Jev. |
| [Use it over MCP](#use-it-over-mcp) | Connect an MCP client. |
| [Write policies](#write-policies) | Define decisions and hard rules. |
| [Store and inspect decisions](#store-and-inspect-decisions) | Keep and query audit records. |
| [Operate it](#operate-it) | Configure and deploy the service. |
| [Give an agent the manual](#give-an-agent-the-manual) | Install the agent skill. |

## Quick start

Ask one simple question: which support queue should receive a message? The caller sends the message. Jev chooses one of the options declared by the policy. Vibecheck turns that answer into an outcome your program can act on.

Create a policy directory and fetch that policy:

```bash
mkdir -p policies
curl -fsSL https://raw.githubusercontent.com/psyb0t/vibecheck/main/examples/support-ticket-router/policy.yaml \
  -o policies/support-ticket-router.yaml
```

Start the container:

```bash
docker run -d --name vibecheck \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -e VIBECHECK_TYPESAFE_API_KEY="$TYPESAFE_API_KEY" \
  -v "$PWD/policies:/config/policies:ro" \
  -v vibecheck-data:/data \
  psyb0t/vibecheck:latest run
```

The image listens on fixed container ports. `-p 127.0.0.1:8080:8080` is the exposure decision: the left side is the host address and port, the right side is the container port. To use host port 8181 instead, write `-p 127.0.0.1:8181:8080`. Metrics stay on the unexposed container port 9091.

`TYPESAFE_API_KEY` must contain a TypeSafe API key. Vibecheck reports not ready when the key is missing, but `/healthz` stays available so an operator can diagnose the configuration.

Check readiness:

```bash
curl -fsS http://127.0.0.1:8080/ready
```

Use `latest` to try Vibecheck. Pin an immutable release such as `psyb0t/vibecheck:v0.4.0` for a deployment you want to keep.

Send the decision request:

```bash
curl -fsS http://127.0.0.1:8080/v1/evaluations \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: 00000000-0000-4000-8000-000000000001' \
  -d '{
    "policyRef": {"name": "support-ticket-router", "version": "1.0.0"},
    "state": {"message": "I was charged twice for the same invoice."}
  }'
```

The response includes this result, plus its stable evaluation ID and audit data:

```json
{
  "id": "<evaluation-id>",
  "outcome": "billing",
  "matchedRuleId": "route-billing",
  "executionPath": "decision_rule",
  "answers": {
    "destination": {
      "type": "choice",
      "choice": "billing",
      "confidence": 0.97,
      "probabilities": {
        "billing": 0.97,
        "technical": 0.02,
        "other": 0.01
      }
    }
  }
}
```

The exact probabilities vary. Your program sees `billing` and puts the message in the billing queue. The caller did not classify the message itself. It supplied raw state, Jev returned a bounded `choice`, and Vibecheck mapped that answer through `route-billing`.

That is the basic loop: declare bounded questions and outcomes, send raw state, then make your caller obey the returned outcome. Add trusted facts only for values your program already knows, such as ownership or explicit authorization. Do not send the classification you expect Jev to produce. Read [policy authoring](docs/policy-authoring.md) for the grammar and [the agent action firewall example](docs/agent-action-firewall.md) for a larger policy.

Reusing an `Idempotency-Key` with the same request returns the original decision without billing the provider again. Reusing it with different input returns `409`.

Set `VIBECHECK_API_TOKEN` to require bearer authentication, then add `Authorization: Bearer <token>` to REST and MCP requests.

## Use it over MCP

Point a Streamable HTTP MCP client at:

```text
http://127.0.0.1:8080/mcp
```

Vibecheck exposes six tools:

- `vibecheck_evaluate`
- `vibecheck_list_policies`
- `vibecheck_get_policy`
- `vibecheck_get_evaluation`
- `vibecheck_validate_policy`
- `vibecheck_submit_feedback`

Both `/mcp` and `/mcp/` reach the handler without a redirect. REST and MCP call the same services and write the same audit records. See [MCP](docs/mcp.md) for client configuration.

## Write policies

Policies are YAML or JSON files mounted read-only at `/config/policies`. A policy declares:

- its name, version, model, valid outcomes, and default outcome;
- typed caller facts that the model cannot reinterpret;
- bounded `choice`, `score`, and `noul` questions for Jev;
- pre-rules that can decide from trusted facts without calling the provider;
- ordered decision rules that map typed answers to an outcome.

Vibecheck compiles every policy at startup. Invalid files stop startup. Policy changes take effect after a container restart. Use `POST /v1/policies/validate` or `vibecheck_validate_policy` to check a candidate without installing it. Read [policy authoring](docs/policy-authoring.md) for the complete grammar.

## Store and inspect decisions

SQLite is the default and stores data in `/data/vibecheck.sqlite`. Mount `/data` as a volume or bind mount if decisions must survive container replacement. PostgreSQL is available for shared deployments.

Raw state and facts are not stored by default. Set `VIBECHECK_STORE_INPUTS=true` only with a valid 32-byte base64 `VIBECHECK_DATA_KEY`. Evaluation and idempotency retention are configurable.

Use `GET /v1/evaluations` to page through decisions, `GET /v1/evaluations/{id}` to inspect one, and `POST /v1/evaluations/{id}/feedback` to record the expected outcome without rewriting the original decision.

## Operate it

The image uses fixed container listeners: REST, MCP, `/healthz`, and `/ready` on `0.0.0.0:8080`, Prometheus metrics on `0.0.0.0:9091`. Docker port publishing controls host exposure. Publish only port 8080, normally to loopback. Do not publish the metrics listener to the internet.

Configuration comes from environment variables and is read once at startup. Invalid values fail startup. [.env.example](.env.example) lists the Compose inputs and operator settings. [Deployment](docs/deployment.md) covers hardening, PostgreSQL, retention, shutdown, and upgrades.

## Give an agent the manual

The bundled skill teaches agents how to call a running Vibecheck, interpret outcomes, validate policies, submit feedback, and deploy the image.

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

## Documentation

- [Policy authoring](docs/policy-authoring.md)
- [REST API](docs/api.md)
- [MCP](docs/mcp.md)
- [Deployment](docs/deployment.md)
- [Security](docs/security.md)
- [Agent action firewall example](docs/agent-action-firewall.md)
- [Architecture](docs/architecture.md)

## Development

Development requires Docker, Make, Git, and a source checkout. Run `make help` for the full target list. The usual checks are `make generate`, `make lint`, `make test`, `make test-api`, `make test-coverage`, and `make sec`. See [testing](docs/testing.md) for the test layers.

## License

MIT. See [LICENSE](LICENSE). Release history is in [CHANGELOG.md](CHANGELOG.md).

*Built with spite using https://github.com/psyb0t/servicepack*
