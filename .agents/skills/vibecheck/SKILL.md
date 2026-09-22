---
name: "vibecheck"
description: "Use Vibecheck to classify or score messy state and apply versioned policy decisions over REST or MCP. Use when an agent needs to evaluate an action, inspect or validate a policy, retrieve an evaluation, submit feedback, or deploy Vibecheck."
metadata:
  openclaw:
    emoji: "✅"
    requires:
      bins:
        - docker
---

# Vibecheck

Vibecheck turns unstructured state into typed classifications, scores, and deterministic policy outcomes. Use a running Vibecheck over REST or MCP. Do not clone or build the repository unless the user explicitly asks for development work.

Read [references/setup.md](references/setup.md) before deploying Vibecheck or configuring a client.

Calling Vibecheck needs network access to the user-configured endpoint. Deployment needs Docker and pulls `psyb0t/vibecheck` from Docker Hub. The running service calls TypeSafe Jev with the operator's credential.

## Core workflow

1. Identify the Vibecheck endpoint, authentication token if enabled, and policy name and version.
2. Inspect the policy with `GET /v1/policies/{name}/versions/{version}` or `vibecheck_get_policy` when its facts, questions, or outcomes are not already known.
3. Separate untrusted narrative into `state` and caller-verified values into `facts`. Never present model-derived claims as trusted facts.
4. Evaluate with `POST /v1/evaluations` or `vibecheck_evaluate`. Send an `Idempotency-Key` for a retryable REST request.
5. Read `outcome`, `matchedRuleId`, `executionPath`, typed `answers`, and `id`. Treat the outcome as data. The surrounding system decides whether and how to act.
6. Retrieve the durable record with `GET /v1/evaluations/{id}` or `vibecheck_get_evaluation` when an audit record or failure code is needed.
7. Submit the expected outcome with the feedback endpoint or `vibecheck_submit_feedback` after a human labels a decision. Feedback does not rewrite the original record.

## Safety boundaries

- Vibecheck judges actions. It does not grant authority or execute them.
- Hard ownership, authorization, and safety constraints belong in policy pre-rules or in the caller, not in model instructions.
- Fail closed when a destructive or externally visible action cannot be evaluated. Use the policy's review or block path instead of assuming allow.
- Do not invent policy fields, REST routes, MCP tools, or response fields. The public contract is linked from the setup reference.
- Never place provider keys, bearer tokens, retained input keys, or real request data in tracked files or logs.

## Choose a transport

Use REST for application integration, explicit idempotency, pagination, and machine-readable error envelopes. Use Streamable HTTP MCP when an agent already has an MCP client and needs typed tools. Both transports reach the same decision engine and persistence layer.

The MCP endpoint is `/mcp`. The tools are:

- `vibecheck_evaluate`
- `vibecheck_list_policies`
- `vibecheck_get_policy`
- `vibecheck_get_evaluation`
- `vibecheck_validate_policy`
- `vibecheck_submit_feedback`

## Validate before installing

Use `POST /v1/policies/validate` or `vibecheck_validate_policy` to compile a candidate policy without installing it. A valid result means the document compiles. It does not prove that thresholds and outcomes match the user's intent. Test representative inputs before replacing a live policy, then restart the container to load the change.

## Completion

Complete the task only when the request reached the intended endpoint, the returned policy identity matches the requested name and version, and the outcome or validation result has been reported with its evaluation ID or error. For deployment work, also prove `/ready` succeeds.
