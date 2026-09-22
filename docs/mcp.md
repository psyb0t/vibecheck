# MCP

Vibecheck exposes a Streamable HTTP MCP server at `/mcp` on the same listener
as the REST API. It is built on the official Go SDK and calls the same core
services the REST handlers call, so an evaluation created over MCP is
byte-identical to the same evaluation created over REST and lands in the same
audit table.

## Endpoint

```
POST http://127.0.0.1:8080/mcp
```

Both `/mcp` and `/mcp/` reach the handler directly. Neither redirects.

That matters more than it looks. If only `/mcp/` were registered, Go's
`net/http` would answer a POST to `/mcp` with a 301, and a redirected POST
drops its body. The router registers both as exact patterns, and a test
asserts no redirect on either form with the client's redirect following
turned off.

Bearer auth, the body limit, the rate limit, and request-ID handling apply to
`/mcp` exactly as they do to the REST paths.

## Tools

| Tool | Purpose |
|---|---|
| `vibecheck_evaluate` | Evaluate state against a loaded or inline policy |
| `vibecheck_list_policies` | List every policy loaded at startup |
| `vibecheck_get_policy` | Fetch one loaded policy by name and version |
| `vibecheck_get_evaluation` | Fetch one recorded evaluation by ID |
| `vibecheck_validate_policy` | Compile a document and report validity, installing nothing |
| `vibecheck_submit_feedback` | Record the outcome a human expected |

Every tool has a typed input and output schema published through
`tools/list`, so a client knows the shape before it calls.

## Client configuration

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

Drop the `headers` block when `VIBECHECK_API_TOKEN` is empty.

## Errors

A tool that fails returns an MCP tool error, not a transport error. The call
completed; the work inside it did not. The result carries `isError` so a
model can react to it, and the message is the core's own text.

The message never contains provider text. A provider error body can echo the
caller's own state back, so the core never puts one in an error and the
transport has nothing to leak.

The stable `{code, message, details}` envelope is a REST concept. Over MCP
the failure arrives as a tool error, so a client that needs the machine-
readable code should read the recorded evaluation through
`vibecheck_get_evaluation` and use its `failureCode`.

Malformed JSON-RPC is a protocol-level error and is answered as one.

## Cancellation

Cancelling an in-flight tool call cancels the work behind it. A cancelled
evaluation stops retrying the provider rather than finishing a call nobody
will read.

## Why an agent should use this

The point of Vibecheck over MCP is that an agent can ask for a judgement it
is not allowed to make for itself. The agent proposes an action, Vibecheck
returns `allow`, `review`, or `block` with the rule that decided it, and the
surrounding system enforces that answer. The agent never receives authority
it did not already have, and every decision leaves a row.

Pre-rules are what make this more than advice from a model. A constraint in
`preRules` is evaluated from trusted facts before the provider is called, so
no amount of persuasive state text can move it. See
[policy authoring](policy-authoring.md).
