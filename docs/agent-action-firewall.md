# Example: agent action firewall

`examples/agent-action-firewall/` is a complete, working policy that judges a
proposed coding-agent action and returns `allow`, `review`, or `block`.

It is an example, not a privileged code path. Vibecheck loads it exactly the
way it loads any other policy and gives it no special treatment.

## What it decides

A caller proposes an action as raw state and supplies two authoritative facts it already knows:

| Fact | Type | Meaning |
|---|---|---|
| `target.ownedBySession` | boolean | Whether the target belongs to this session |
| `authorization.explicit` | boolean | Whether a human explicitly approved it |

`target.path` is optional.

The policy asks three typed questions: whether the action is destructive
(`noul`), how broad the impact could be (`score` over four ordered rungs),
and which class of action it is (`choice` over five named keys).

## The ownership floor

One pre-rule runs before the provider is called:

```yaml
  preRules:
    - id: block-unowned-unauthorized-target
      description: An unowned target needs explicit authorization.
      when:
        all:
          - left: {fact: target.ownedBySession}
            op: eq
            right: false
          - left: {fact: authorization.explicit}
            op: eq
            right: false
      outcome: block
```

An action on a target the session does not own is blocked unless a human explicitly authorized it. No provider call happens, so nothing in the caller's `state` is ever read, and no text in it can argue with the result.

That is the difference between this and asking a model nicely. The hard
constraint sits outside the model's reach by construction.

## The decision rules

Four rules, in order, first match wins:

1. `review-ambiguous-action`. The class came back `unknown`, or confidence
   is under 0.65. An unclear classification goes to a human.
2. `block-high-risk-without-authorization`. Blast radius at 2.5 or above
   with no explicit authorization.
3. `review-meaningful-destructive-probability`. A material probability for
   `destructive_write` reaches review even when the selected class is not
   destructive.
4. `allow-owned-low-risk-action`. Owned target, destructive probability under
   0.35, blast radius under 1.5.

No match means the declared default, which is `review`. The safe answer is
the fallback, not the exception.

## Acceptance vectors

`acceptance.yaml` holds 13 labelled vectors with their expected outcome,
matching rule, and execution path. The test suite compiles the policy and
runs every vector, so the shipped example is verified rather than
illustrative.

The vectors fix the model's answers. What they test is the deterministic
part: fact validation, pre-rule short-circuiting, decision-rule ordering, and
the default outcome. How often the provider returns a given answer for a
given state is a separate, opt-in exercise against the live API. A
probabilistic output cannot be a CI assertion.

Among them are a missing required fact, a fact of the wrong type, and
adversarial `state` text that instructs the model to approve a destructive
action on an unowned target. The pre-rule blocks that one and the provider
never sees the instruction.

Every vector is synthetic. No real repository path, hostname, or user text
appears in the file.

## Running it

Copy the policy into your policy directory:

```bash
mkdir -p ./policies
cp examples/agent-action-firewall/policy.yaml ./policies/
VIBECHECK_POLICY_HOST_DIR=./policies docker compose up -d
```

Then evaluate an action:

```bash
curl -sS http://127.0.0.1:8080/v1/evaluations \
  -H 'Content-Type: application/json' \
  -d '{
    "policyRef": {"name": "agent-action-firewall", "version": "1.0.0"},
    "state": {"command": "rm -rf /etc/nginx"},
    "facts": {
      "target.ownedBySession": false,
      "authorization.explicit": false
    }
  }'
```

That returns `block` with `matchedRuleId: block-unowned-unauthorized-target`
and `executionPath: pre_rule`, and `usage` shows no tokens spent.

## Using the answer

Vibecheck told you what it thinks. It did not stop anything.

Wire the outcome into whatever actually gates the action, and fail closed: if
Vibecheck is unreachable or returns a provider failure, treat a destructive
action as `review` or `block`. An advisory service that gets treated as
optional when it is down is not a boundary.

See [security](security.md) for the reasoning, and
[policy authoring](policy-authoring.md) for the full grammar.
