package policy_test

import (
	"math"
	"testing"

	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatePreRulesShortCircuits(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	facts := validFacts()
	facts[factDestructive] = true
	facts[factOwned] = false

	result, matched := compiled.EvaluatePreRules(facts)
	require.True(
		t,
		matched,
		"a destructive action on an unowned target must short-circuit",
	)
	assert.Equal(t, outcomeBlock, result.Outcome)
	assert.Equal(t, rulePreBlockUnowned, result.MatchedRuleID)
	assert.Equal(t, decision.ExecutionPathPreRule, result.Path)
}

func TestEvaluatePreRulesReportsNoMatch(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	_, matched := compiled.EvaluatePreRules(validFacts())
	assert.False(
		t, matched,
		"no pre-rule match is how the caller learns it still has to ask the provider", //nolint:lll // one string literal; splitting it would change the text
	)
}

func TestEvaluatePreRulesTakesTheFirstMatch(t *testing.T) {
	t.Parallel()

	doc := mutate(func(d *policy.Document) {
		d.Spec.PreRules = []policy.Rule{
			{
				ID: "first-wins",
				When: &policy.Condition{
					Left:  &policy.Operand{Fact: factOwned},
					Op:    policy.OperatorEq,
					Right: true,
				},
				Outcome: outcomeAllow,
			},
			{
				ID: "second-also-matches",
				When: &policy.Condition{
					Left:  &policy.Operand{Fact: factOwned},
					Op:    policy.OperatorEq,
					Right: true,
				},
				Outcome: outcomeBlock,
			},
		}
	})

	compiled, err := policy.Compile(doc)
	require.NoError(t, err)

	result, matched := compiled.EvaluatePreRules(validFacts())
	require.True(t, matched)
	assert.Equal(t, "first-wins", result.MatchedRuleID)
	assert.Equal(t, outcomeAllow, result.Outcome)
}

func TestEvaluateDecisionRules(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	testCases := []struct {
		name        string
		answers     decision.Answers
		wantOutcome decision.Outcome
		wantRule    string
		wantPath    decision.ExecutionPath
	}{
		{
			name: "first matching rule wins even though a later one would also match", //nolint:lll // one string literal; splitting it would change the text
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Choice = choiceUnknown
				answers[questionClass] = class

				return answers
			}(),
			wantOutcome: outcomeReview,
			wantRule:    ruleReviewUnknown,
			wantPath:    decision.ExecutionPathDecisionRule,
		},
		{
			name:        "second rule matches when the first does not",
			answers:     validAnswers(),
			wantOutcome: outcomeAllow,
			wantRule:    ruleAllowLowRisk,
			wantPath:    decision.ExecutionPathDecisionRule,
		},
		{
			name: "default applies when nothing matches",
			answers: func() decision.Answers {
				answers := validAnswers()
				risky := answers[questionRisky]
				risky.Noul = 0.9
				answers[questionRisky] = risky

				return answers
			}(),
			wantOutcome: outcomeReview,
			wantRule:    "",
			wantPath:    decision.ExecutionPathDefault,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := compiled.EvaluateDecisionRules(validFacts(), tc.answers)
			assert.Equal(t, tc.wantOutcome, result.Outcome)
			assert.Equal(t, tc.wantRule, result.MatchedRuleID)
			assert.Equal(t, tc.wantPath, result.Path)
		})
	}
}

func TestValidateFactsAcceptsGoodInput(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	testCases := []struct {
		name  string
		facts decision.Facts
	}{
		{"exactly the required facts", validFacts()},
		{
			"an integer for a number fact",
			func() decision.Facts {
				facts := validFacts()
				facts[factCount] = 3

				return facts
			}(),
		},
		{
			"an optional fact supplied",
			func() decision.Facts {
				facts := validFacts()
				facts[factPath] = testFactPathTemp

				return facts
			}(),
		},
		{
			"an undeclared fact under non-strict input",
			func() decision.Facts {
				facts := validFacts()
				facts["something.else"] = "ignored"

				return facts
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, compiled.ValidateFacts(tc.facts))
		})
	}
}

func TestValidateFactsRejectsBadInput(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	strictDoc := mutate(func(d *policy.Document) { d.Spec.Input.Strict = true })
	strict, err := policy.Compile(strictDoc)
	require.NoError(t, err)

	testCases := []struct {
		name     string
		compiled *policy.Compiled
		facts    decision.Facts
	}{
		{
			name:     "missing required fact",
			compiled: compiled,
			facts: func() decision.Facts {
				facts := validFacts()
				delete(facts, factOwned)

				return facts
			}(),
		},
		{
			name:     "wrong type for a boolean fact",
			compiled: compiled,
			facts: func() decision.Facts {
				facts := validFacts()
				facts[factOwned] = "true"

				return facts
			}(),
		},
		{
			name:     "wrong type for a number fact",
			compiled: compiled,
			facts: func() decision.Facts {
				facts := validFacts()
				facts[factCount] = "one"

				return facts
			}(),
		},
		{
			name:     "wrong type for a string fact",
			compiled: compiled,
			facts: func() decision.Facts {
				facts := validFacts()
				facts[factKind] = 7

				return facts
			}(),
		},
		{
			name:     "wrong type for a supplied optional fact",
			compiled: compiled,
			facts: func() decision.Facts {
				facts := validFacts()
				facts[factPath] = 7

				return facts
			}(),
		},
		{
			name:     "undeclared fact under strict input",
			compiled: strict,
			facts: func() decision.Facts {
				facts := validFacts()
				facts["something.else"] = "rejected"

				return facts
			}(),
		},
		{
			name:     "no facts at all",
			compiled: compiled,
			facts:    decision.Facts{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.compiled.ValidateFacts(tc.facts)
			require.ErrorIs(t, err, commerr.ErrValidationFailed)
		})
	}
}

func TestValidateAnswersAcceptsAGoodResponse(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)
	require.NoError(t, compiled.ValidateAnswers(validAnswers()))
}

func TestValidateAnswersAcceptsDistributionsWithinTolerance(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	// The distribution is spread across two options so that no single value
	// leaves the unit interval; the sum is what is being probed here.
	half := policy.ProbabilitySumTolerance / 2

	testCases := []struct {
		name  string
		read  float64
		write float64
	}{
		{"sum exactly at the lower tolerance edge", 0.5 - half, 0.5 - half},
		{"sum exactly at the upper tolerance edge", 0.5 + half, 0.5 + half},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers := validAnswers()
			class := answers[questionClass]
			class.Probabilities = map[string]float64{
				choiceRead:    tc.read,
				choiceWrite:   tc.write,
				choiceUnknown: 0,
			}
			answers[questionClass] = class

			require.NoError(t, compiled.ValidateAnswers(answers))
		})
	}
}

func TestValidateAnswersRejectsDistributionsOutsideTolerance(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	// One percentage point beyond the tolerance on each side.
	outside := policy.ProbabilitySumTolerance/2 + 0.01

	testCases := []struct {
		name  string
		read  float64
		write float64
	}{
		{"sum below the tolerance", 0.5 - outside, 0.5 - outside},
		{"sum above the tolerance", 0.5 + outside, 0.5 + outside},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers := validAnswers()
			class := answers[questionClass]
			class.Probabilities = map[string]float64{
				choiceRead:    tc.read,
				choiceWrite:   tc.write,
				choiceUnknown: 0,
			}
			answers[questionClass] = class

			require.ErrorIs(
				t,
				compiled.ValidateAnswers(answers),
				policy.ErrProviderProtocol,
			)
		})
	}
}

func TestValidateAnswersRejectsProtocolViolations(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	testCases := []struct {
		name    string
		answers decision.Answers
	}{
		{
			name: "an extra answer nobody asked for",
			answers: func() decision.Answers {
				answers := validAnswers()
				answers["surprise"] = decision.Answer{
					Type: decision.QuestionTypeNoul, Noul: 0.5,
				}

				return answers
			}(),
		},
		{
			name: "a missing answer",
			answers: func() decision.Answers {
				answers := validAnswers()
				delete(answers, questionRisky)

				return answers
			}(),
		},
		{
			name: "a renamed answer keeping the count",
			answers: func() decision.Answers {
				answers := validAnswers()
				answers["renamed"] = answers[questionRisky]
				delete(answers, questionRisky)

				return answers
			}(),
		},
		{
			name: "the wrong answer type",
			answers: func() decision.Answers {
				answers := validAnswers()
				answers[questionRisky] = decision.Answer{
					Type: decision.QuestionTypeChoice, Choice: choiceRead,
				}

				return answers
			}(),
		},
		{
			name: "a noul outside zero to one",
			answers: func() decision.Answers {
				answers := validAnswers()
				risky := answers[questionRisky]
				risky.Noul = 1.5
				answers[questionRisky] = risky

				return answers
			}(),
		},
		{
			name: "a NaN noul",
			answers: func() decision.Answers {
				answers := validAnswers()
				risky := answers[questionRisky]
				risky.Noul = math.NaN()
				answers[questionRisky] = risky

				return answers
			}(),
		},
		{
			name: "a choice option the question does not declare",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Choice = "invented"
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "a choice with no confidence",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.HasConfidence = false
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "a confidence outside zero to one",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Confidence = 1.2
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "the wrong number of choice probabilities",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Probabilities = map[string]float64{choiceRead: 1.0}
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "a choice probability key the question does not declare",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Probabilities = map[string]float64{
					choiceRead: 0.8, choiceWrite: 0.15, "invented": 0.05,
				}
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "a choice distribution that does not sum to one",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Probabilities = map[string]float64{
					choiceRead: 0.2, choiceWrite: 0.2, choiceUnknown: 0.2,
				}
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "a probability outside zero to one",
			answers: func() decision.Answers {
				answers := validAnswers()
				class := answers[questionClass]
				class.Probabilities = map[string]float64{
					choiceRead: 1.4, choiceWrite: -0.2, choiceUnknown: -0.2,
				}
				answers[questionClass] = class

				return answers
			}(),
		},
		{
			name: "a score above the top level",
			answers: func() decision.Answers {
				answers := validAnswers()
				spread := answers[questionSpread]
				spread.Score = 3
				answers[questionSpread] = spread

				return answers
			}(),
		},
		{
			name: "a negative score",
			answers: func() decision.Answers {
				answers := validAnswers()
				spread := answers[questionSpread]
				spread.Score = -0.1
				answers[questionSpread] = spread

				return answers
			}(),
		},
		{
			name: "an infinite score",
			answers: func() decision.Answers {
				answers := validAnswers()
				spread := answers[questionSpread]
				spread.Score = math.Inf(1)
				answers[questionSpread] = spread

				return answers
			}(),
		},
		{
			name: "the wrong number of score levels in the legend",
			answers: func() decision.Answers {
				answers := validAnswers()
				spread := answers[questionSpread]
				spread.Legend = map[string]any{"0": "a", "1": "b"}
				answers[questionSpread] = spread

				return answers
			}(),
		},
		{
			name: "a score legend missing a level key",
			answers: func() decision.Answers {
				answers := validAnswers()
				spread := answers[questionSpread]
				spread.Legend = map[string]any{"0": "a", "1": "b", "9": "c"}
				answers[questionSpread] = spread

				return answers
			}(),
		},
		{
			name: "score probabilities missing a level key",
			answers: func() decision.Answers {
				answers := validAnswers()
				spread := answers[questionSpread]
				spread.Probabilities = map[string]float64{
					"0": 0.5, "1": 0.5, "9": 0.0,
				}
				answers[questionSpread] = spread

				return answers
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := compiled.ValidateAnswers(tc.answers)
			require.ErrorIs(t, err, policy.ErrProviderProtocol)
		})
	}
}

func TestValidateAnswersAcceptsScoreAtTheLevelBoundaries(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	testCases := []struct {
		name  string
		score float64
	}{
		{"bottom level", 0},
		{"between levels", 1.25},
		{"top level", 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers := validAnswers()
			spread := answers[questionSpread]
			spread.Score = tc.score
			answers[questionSpread] = spread

			require.NoError(t, compiled.ValidateAnswers(answers))
		})
	}
}
