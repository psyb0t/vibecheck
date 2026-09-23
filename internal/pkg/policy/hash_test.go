package policy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashIsStableAcrossReformatting(t *testing.T) {
	t.Parallel()

	compact := minimalYAML("hash-policy", testPolicyVersion)
	reformatted := strings.ReplaceAll(compact, "\n", "\n\n") +
		"\n# a trailing comment that changes nothing\n"

	first, err := policy.CompileBytes([]byte(compact), 0)
	require.NoError(t, err)

	second, err := policy.CompileBytes([]byte(reformatted), 0)
	require.NoError(t, err)

	assert.Equal(
		t, first.Hash, second.Hash,
		"whitespace and comments are not behavior, so they must not move the hash", //nolint:lll // one string literal; splitting it would change the text
	)
}

func TestHashIsStableAcrossRuns(t *testing.T) {
	t.Parallel()

	first, err := policy.Compile(validDocument())
	require.NoError(t, err)

	second, err := policy.Compile(validDocument())
	require.NoError(t, err)

	assert.Equal(t, first.Hash, second.Hash)
	assert.JSONEq(t, string(first.Canonical), string(second.Canonical))
}

func TestHashChangesWithBehavior(t *testing.T) {
	t.Parallel()

	baseline := compileValid(t)

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"a different version",
			mutate(func(d *policy.Document) { d.Metadata.Version = "1.0.1" }),
		},
		{
			"a different default outcome",
			mutate(func(d *policy.Document) {
				d.Spec.DefaultOutcome = outcomeBlock
			}),
		},
		{
			"a reworded instruction",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionRisky]
				question.Instructions = "Is the action reversible?"
				d.Spec.Questions[questionRisky] = question
			}),
		},
		{
			"a changed rule threshold",
			mutate(func(d *policy.Document) {
				d.Spec.DecisionRules[1].When.All[1].Right = 0.5
			}),
		},
		{
			"a different probability selector",
			mutate(func(d *policy.Document) {
				d.Spec.DecisionRules[0].When = &policy.Condition{
					Left: &policy.Operand{
						Answer:         questionClass,
						Field:          decision.AnswerFieldProbability,
						ProbabilityKey: choiceWrite,
					},
					Op:    policy.OperatorGte,
					Right: 0.2,
				}
			}),
		},
		{
			"a reordered rule list",
			mutate(func(d *policy.Document) {
				rules := d.Spec.DecisionRules
				rules[0], rules[1] = rules[1], rules[0]
			}),
		},
		{
			"an added optional fact",
			mutate(func(d *policy.Document) {
				facts := d.Spec.Input.OptionalFacts
				facts["target.size"] = decision.FactTypeNumber
			}),
		},
		{
			"strict input turned on",
			mutate(func(d *policy.Document) { d.Spec.Input.Strict = true }),
		},
		{
			"a different model",
			mutate(func(d *policy.Document) { d.Spec.Model = "jev-preview" }),
		},
		{
			"a reworded description",
			mutate(func(d *policy.Document) {
				d.Metadata.Description = "something else"
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			changed, err := policy.Compile(tc.doc)
			require.NoError(t, err)
			assert.NotEqual(t, baseline.Hash, changed.Hash)
		})
	}
}

func TestHashChangesWithProbabilitySelector(t *testing.T) {
	t.Parallel()

	compileWithSelector := func(
		t *testing.T,
		probabilityKey string,
	) *policy.Compiled {
		t.Helper()

		doc := mutate(func(d *policy.Document) {
			d.Spec.DecisionRules[0].When = &policy.Condition{
				Left: &policy.Operand{
					Answer:         questionClass,
					Field:          decision.AnswerFieldProbability,
					ProbabilityKey: probabilityKey,
				},
				Op:    policy.OperatorGte,
				Right: 0.2,
			}
		})

		compiled, err := policy.Compile(doc)
		require.NoError(t, err)

		return compiled
	}

	read := compileWithSelector(t, choiceRead)
	write := compileWithSelector(t, choiceWrite)
	assert.NotEqual(t, read.Hash, write.Hash)
}

func TestQuestionOrderDoesNotChangeTheHash(t *testing.T) {
	t.Parallel()

	baseline := compileValid(t)

	// Go map iteration order is already random, so recompiling the identical
	// document repeatedly is the real test that question ordering is
	// normalised rather than incidental.
	for range 16 {
		repeat, err := policy.Compile(validDocument())
		require.NoError(t, err)
		require.Equal(t, baseline.Hash, repeat.Hash)
	}
}

func TestCanonicalFormIsValidJSONCarryingTheIdentity(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	var form map[string]any
	require.NoError(t, json.Unmarshal(compiled.Canonical, &form))

	assert.Equal(t, policy.APIVersion, form["apiVersion"])
	assert.Equal(t, policy.Kind, form["kind"])
	assert.Equal(t, compiled.Ref.Name, form["name"])
	assert.Equal(t, compiled.Ref.Version, form["version"])
	assert.Len(t, compiled.Hash, 64, "sha-256 renders as 64 hex characters")
}
