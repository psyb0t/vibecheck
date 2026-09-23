# Policy authoring

A policy is a YAML or JSON document that declares what questions to ask, what
outcomes exist, and which deterministic rules map answers to an outcome.
Vibecheck compiles every policy at startup. A policy that does not compile
stops the process, so a typo fails the deployment instead of silently
disabling a rule.

Mount a directory of policies at `VIBECHECK_POLICY_DIR`. Files ending in
`.yaml` or `.yml` are policies; anything else in the directory is ignored.
Symlinks and non-regular files are refused.

## Document shape

```yaml
apiVersion: vibecheck.psyb0t.dev/v1alpha1
kind: DecisionPolicy
metadata:
  name: agent-action-firewall
  version: 1.0.0
  description: Judge a proposed coding-agent action.
spec:
  model: jev-latest
  outcomes: [allow, review, block]
  defaultOutcome: review
  input: {}
  preRules: []
  questions: {}
  decisionRules: []
```

A policy is identified by name and version together. Two files may share a
name at different versions; two files with the same name and version are a
startup error.

`outcomes` is the closed set of answers the policy can produce.
`defaultOutcome` must be one of them and is what a request gets when no
decision rule matches.

## Facts

Facts are values the caller already knows and the model never sees as
negotiable. Declare their types so a wrong type is rejected before any
provider call.

```yaml
  input:
    requiredFacts:
      action.destructive: boolean
      target.ownedBySession: boolean
    optionalFacts:
      target.path: string
```

Types are `string`, `boolean`, and `number`. A missing required fact or a
fact of the wrong type fails the request with a validation error.

## Questions

Each question has a type and instructions. The question name is how rules
refer to the answer.

```yaml
  questions:
    destructive:
      type: noul
      instructions: Is the proposed action destructive or hard to reverse?

    blastRadius:
      type: score
      instructions: How broad is the potential impact of this action?
      criteria:
        - Confined to one disposable item
        - Confined to the current project
        - Could affect unrelated user work
        - Could affect the host or external systems

    actionClass:
      type: choice
      instructions: Which class best describes the proposed action?
      criteria:
        read: Reads state without changing it
        destructive_write: Deletes or irreversibly replaces state
        unknown: None of the other classes clearly applies
```

| Type | Answer fields a rule may read | Meaning |
|---|---|---|
| `noul` | `noul` | A truth value in `[0, 1]` |
| `score` | `score`, `confidence`, `probability` | An index into the ordered `criteria` list and a probability for one level |
| `choice` | `choice`, `confidence`, `probability` | One key from the `criteria` map and a probability for one option |

A `noul` answer has no `confidence` field. The value already is the model's
graded belief, so a separate confidence on top of it would be a second,
unanchored number saying the same thing. Naming `confidence` on a `noul`
question is a compile error.

`score` criteria are an ordered list, so the returned score is positional and
comparisons like `gte: 2.5` mean "at least between the third and fourth
rung". `choice` criteria are a map, because a choice is named, not ranked.

## Conditions

A condition compares a left operand to a right value.

```yaml
      when:
        all:
          - left: {fact: target.ownedBySession}
            op: eq
            right: true
          - left: {answer: blastRadius, field: score}
            op: gte
            right: 2.5
```

The left operand is either `{fact: <name>}` or
`{answer: <question>, field: <field>}`. Operators are `eq`, `neq`, `lt`,
`lte`, `gt`, `gte`, `in`, and `exists`. Combine conditions with `all` or
`any`.

Use `field: probability` when a rule needs the probability of one candidate, including a candidate that the provider did not select. It needs `probabilityKey`. On a `choice` question this is a declared option key. On a `score` question it is the zero-based level index written as a canonical decimal string.

```yaml
      when:
        left:
          answer: actionClass
          field: probability
          probabilityKey: destructive_write
        op: gte
        right: 0.2
```

This checks the probability assigned to `destructive_write`, not whether `actionClass.choice` equals that key. `noul` does not support `field: probability` because its `noul` value already is the probability of its proposition.

The compiler type-checks both sides. Comparing a boolean fact with `gte`, or
naming a question, choice option, or score level that does not exist, is a compile error. Probability thresholds must be in `[0, 1]`.

## Pre-rules and decision rules

Pre-rules run before the provider is called and see only facts. A matching
pre-rule settles the outcome, so the provider is never asked and no tokens
are spent.

```yaml
  preRules:
    - id: block-unowned-destructive-target
      description: A model cannot override the ownership floor.
      when:
        all:
          - left: {fact: action.destructive}
            op: eq
            right: true
          - left: {fact: target.ownedBySession}
            op: eq
            right: false
      outcome: block
```

Put every hard constraint here. A rule in `preRules` is outside the model's
reach by construction, which is the difference between a boundary and a
suggestion.

Decision rules run after the answers arrive, in declared order. The first
match wins and its `id` is returned to the caller as the reason.

## Versioning

Change a rule, get a new version. The canonical hash covers semantic content,
so reindenting a file or reordering map keys leaves the hash alone while
changing a threshold does not. Evaluations record the hash, so an audit trail
survives a policy edit.

## Validating before deploying

`POST /v1/policies/validate` compiles a candidate document and reports the
result without installing it. A document that fails returns
`{"valid": false, "error": "..."}` with a 200, because the question the
caller asked was answered.

## Worked example

`examples/agent-action-firewall/` holds a complete policy plus
`acceptance.yaml`, a set of labelled input vectors with their expected
outcomes. The test suite compiles the policy and runs every vector, so the
shipped example is verified rather than illustrative. See
[the example walkthrough](agent-action-firewall.md).
