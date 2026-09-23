package policy_test

import (
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withPreCondition replaces the single pre-rule's condition, which is the
// cheapest way to compile one condition in isolation.
func withPreCondition(condition *policy.Condition) *policy.Document {
	return mutate(func(d *policy.Document) {
		d.Spec.PreRules[0].When = condition
		d.Spec.DecisionRules = nil
	})
}

// withDecisionCondition replaces the first decision rule's condition, for the
// cases that need answers in scope.
func withDecisionCondition(condition *policy.Condition) *policy.Document {
	return mutate(func(d *policy.Document) {
		d.Spec.DecisionRules = d.Spec.DecisionRules[:1]
		d.Spec.DecisionRules[0].When = condition
	})
}

func TestCompileRejectsMalformedConditions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		condition *policy.Condition
	}{
		{"no branch", &policy.Condition{}},
		{
			"two branches",
			&policy.Condition{
				All:  []policy.Condition{factLeaf()},
				Left: &policy.Operand{Fact: factOwned},
			},
		},
		{
			"unsupported operator",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.Operator("between"),
				Right: 1,
			},
		},
		{
			"left with neither fact nor answer",
			&policy.Condition{
				Left:  &policy.Operand{},
				Op:    policy.OperatorEq,
				Right: true,
			},
		},
		{
			"left with both fact and answer",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factOwned, Answer: questionRisky},
				Op:    policy.OperatorEq,
				Right: true,
			},
		},
		{
			"fact operand carrying a field",
			&policy.Condition{
				Left: &policy.Operand{
					Fact:  factOwned,
					Field: decision.AnswerFieldNoul,
				},
				Op:    policy.OperatorEq,
				Right: true,
			},
		},
		{
			"fact operand carrying a probability selector",
			&policy.Condition{
				Left: &policy.Operand{
					Fact:           factOwned,
					ProbabilityKey: choiceRead,
				},
				Op:    policy.OperatorEq,
				Right: true,
			},
		},
		{
			"missing right operand",
			&policy.Condition{
				Left: &policy.Operand{Fact: factOwned},
				Op:   policy.OperatorEq,
			},
		},
		{
			"in with a scalar right",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorIn,
				Right: choiceRead,
			},
		},
		{
			"in with an empty list",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorIn,
				Right: []any{},
			},
		},
		{
			"in one over the list ceiling",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorIn,
				Right: tooManyCandidates(),
			},
		},
		{
			"nesting one level too deep",
			nestedCondition(policy.MaxConditionDepth + 1),
		},
		{
			"combinator one operand over the ceiling",
			&policy.Condition{
				All: repeatedLeaves(policy.MaxConditionsPerOp + 1),
			},
		},
		{
			"exists with a non-boolean right",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factPath},
				Op:    policy.OperatorExists,
				Right: "yes",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(withPreCondition(tc.condition))
			require.ErrorIs(t, err, policy.ErrInvalidCondition)
		})
	}
}

func tooManyCandidates() []any {
	candidates := make([]any, 0, policy.MaxRightListLength+1)
	for i := range policy.MaxRightListLength + 1 {
		candidates = append(candidates, float64(i))
	}

	return candidates
}

func TestCompileAcceptsConditionsAtTheBoundaries(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		condition *policy.Condition
	}{
		{
			"nesting at the depth ceiling",
			nestedCondition(policy.MaxConditionDepth),
		},
		{
			"combinator at the operand ceiling",
			&policy.Condition{All: repeatedLeaves(policy.MaxConditionsPerOp)},
		},
		{
			"in with one candidate",
			&policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorIn,
				Right: []any{choiceRead},
			},
		},
		{
			"exists with no right operand defaults to present",
			&policy.Condition{
				Left: &policy.Operand{Fact: factPath},
				Op:   policy.OperatorExists,
			},
		},
		{
			"not inverts a leaf",
			&policy.Condition{Not: &policy.Condition{
				Left:  &policy.Operand{Fact: factOwned},
				Op:    policy.OperatorEq,
				Right: true,
			}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(withPreCondition(tc.condition))
			require.NoError(t, err)
		})
	}
}

func TestCompileRejectsUnknownReferences(t *testing.T) {
	t.Parallel()

	t.Run("undeclared fact", func(t *testing.T) {
		t.Parallel()

		_, err := policy.Compile(withPreCondition(&policy.Condition{
			Left:  &policy.Operand{Fact: "action.nope"},
			Op:    policy.OperatorEq,
			Right: true,
		}))
		require.ErrorIs(t, err, policy.ErrUnknownReference)
	})

	t.Run("undeclared question", func(t *testing.T) {
		t.Parallel()

		_, err := policy.Compile(withDecisionCondition(&policy.Condition{
			Left: &policy.Operand{
				Answer: "nope",
				Field:  decision.AnswerFieldNoul,
			},
			Op:    policy.OperatorLt,
			Right: 0.5,
		}))
		require.ErrorIs(t, err, policy.ErrUnknownReference)
	})

	t.Run("undeclared choice option", func(t *testing.T) {
		t.Parallel()

		_, err := policy.Compile(withDecisionCondition(&policy.Condition{
			Left: &policy.Operand{
				Answer: questionClass,
				Field:  decision.AnswerFieldChoice,
			},
			Op:    policy.OperatorEq,
			Right: "nope",
		}))
		require.ErrorIs(t, err, policy.ErrUnknownReference)
	})

	t.Run("undeclared probability option", func(t *testing.T) {
		t.Parallel()

		_, err := policy.Compile(withDecisionCondition(&policy.Condition{
			Left: &policy.Operand{
				Answer:         questionClass,
				Field:          decision.AnswerFieldProbability,
				ProbabilityKey: "nope",
			},
			Op:    policy.OperatorGte,
			Right: 0.2,
		}))
		require.ErrorIs(t, err, policy.ErrUnknownReference)
	})
}

func TestCompileAcceptsProbabilitySelectors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		questionID     string
		probabilityKey string
		right          float64
	}{
		{
			name:           "choice option at the zero threshold",
			questionID:     questionClass,
			probabilityKey: choiceWrite,
			right:          0,
		},
		{
			name:           "score level at the one threshold",
			questionID:     questionSpread,
			probabilityKey: "1",
			right:          1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(withDecisionCondition(&policy.Condition{
				Left: &policy.Operand{
					Answer:         tc.questionID,
					Field:          decision.AnswerFieldProbability,
					ProbabilityKey: tc.probabilityKey,
				},
				Op:    policy.OperatorGte,
				Right: tc.right,
			}))
			require.NoError(t, err)
		})
	}
}

func TestCompileRejectsInvalidProbabilitySelectors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		questionID     string
		field          decision.AnswerField
		probabilityKey string
		wantErr        error
	}{
		{
			name:       "missing selector",
			questionID: questionClass,
			field:      decision.AnswerFieldProbability,
			wantErr:    policy.ErrInvalidCondition,
		},
		{
			name:           "selector on the choice field",
			questionID:     questionClass,
			field:          decision.AnswerFieldChoice,
			probabilityKey: choiceRead,
			wantErr:        policy.ErrInvalidCondition,
		},
		{
			name:           "noncanonical score level selector",
			questionID:     questionSpread,
			field:          decision.AnswerFieldProbability,
			probabilityKey: "01",
			wantErr:        policy.ErrInvalidCondition,
		},
		{
			name:           "undeclared score level selector",
			questionID:     questionSpread,
			field:          decision.AnswerFieldProbability,
			probabilityKey: "9",
			wantErr:        policy.ErrUnknownReference,
		},
		{
			name:           "probability on noul",
			questionID:     questionRisky,
			field:          decision.AnswerFieldProbability,
			probabilityKey: "true",
			wantErr:        policy.ErrTypeMismatch,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(withDecisionCondition(&policy.Condition{
				Left: &policy.Operand{
					Answer:         tc.questionID,
					Field:          tc.field,
					ProbabilityKey: tc.probabilityKey,
				},
				Op:    policy.OperatorGte,
				Right: 0.2,
			}))
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCompileRejectsTypeMismatches(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		condition *policy.Condition
		decision  bool
	}{
		{
			name: "ordering on a string fact",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorGt,
				Right: choiceRead,
			},
		},
		{
			name: "ordering on a boolean fact",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factOwned},
				Op:    policy.OperatorGte,
				Right: true,
			},
		},
		{
			name: "string right for a number fact",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorEq,
				Right: "one",
			},
		},
		{
			name: "number right for a string fact",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorEq,
				Right: 1,
			},
		},
		{
			name: "string right for a boolean fact",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factOwned},
				Op:    policy.OperatorEq,
				Right: "true",
			},
		},
		{
			name: "in with a wrongly typed candidate",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorIn,
				Right: []any{choiceRead, 7},
			},
		},
		{
			name:     "noul field on a choice question",
			decision: true,
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionClass,
					Field:  decision.AnswerFieldNoul,
				},
				Op:    policy.OperatorLt,
				Right: 0.5,
			},
		},
		{
			name:     "confidence field on a noul question",
			decision: true,
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionRisky,
					Field:  decision.AnswerFieldConfidence,
				},
				Op:    policy.OperatorLt,
				Right: 0.5,
			},
		},
		{
			name:     "score field on a noul question",
			decision: true,
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionRisky,
					Field:  decision.AnswerFieldScore,
				},
				Op:    policy.OperatorLt,
				Right: 0.5,
			},
		},
		{
			name:     "ordering on a choice answer",
			decision: true,
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionClass,
					Field:  decision.AnswerFieldChoice,
				},
				Op:    policy.OperatorGt,
				Right: choiceRead,
			},
		},
		{
			name:     "numeric right for a choice answer",
			decision: true,
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionClass,
					Field:  decision.AnswerFieldChoice,
				},
				Op:    policy.OperatorEq,
				Right: 1,
			},
		},
		{
			name:     "string right for a score answer",
			decision: true,
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionSpread,
					Field:  decision.AnswerFieldScore,
				},
				Op:    policy.OperatorGte,
				Right: "high",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			doc := withPreCondition(tc.condition)
			if tc.decision {
				doc = withDecisionCondition(tc.condition)
			}

			_, err := policy.Compile(doc)
			require.ErrorIs(t, err, policy.ErrTypeMismatch)
		})
	}
}

func TestCompileRejectsOutOfRangeProbabilityThresholds(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		field decision.AnswerField
		id    string
		key   string
		right any
	}{
		{"noul above one", decision.AnswerFieldNoul, questionRisky, "", 1.5},
		{"noul below zero", decision.AnswerFieldNoul, questionRisky, "", -0.1},
		{
			"confidence above one",
			decision.AnswerFieldConfidence,
			questionClass,
			"",
			2.0,
		},
		{
			"confidence below zero",
			decision.AnswerFieldConfidence,
			questionClass,
			"",
			-1.0,
		},
		{
			"choice probability above one",
			decision.AnswerFieldProbability,
			questionClass,
			choiceWrite,
			1.1,
		},
		{
			"score probability below zero",
			decision.AnswerFieldProbability,
			questionSpread,
			"1",
			-0.1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(withDecisionCondition(&policy.Condition{
				Left: &policy.Operand{
					Answer:         tc.id,
					Field:          tc.field,
					ProbabilityKey: tc.key,
				},
				Op:    policy.OperatorLt,
				Right: tc.right,
			}))
			require.ErrorIs(t, err, policy.ErrInvalidCondition)
		})
	}
}

func TestCompileKeepsAnswersOutOfPreRules(t *testing.T) {
	t.Parallel()

	doc := withPreCondition(&policy.Condition{
		Left: &policy.Operand{
			Answer: questionRisky,
			Field:  decision.AnswerFieldNoul,
		},
		Op:    policy.OperatorLt,
		Right: 0.5,
	})

	_, err := policy.Compile(doc)
	require.ErrorIs(
		t, err, policy.ErrInvalidCondition,
		"a pre-rule that could read an answer would put the deterministic floor under model control", //nolint:lll // assertion message stays intact; splitting a string literal to satisfy line length is banned
	)
}

func TestConditionEvaluationSemantics(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		condition *policy.Condition
		facts     decision.Facts
		want      bool
	}{
		{
			name: "eq on a matching boolean",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factOwned},
				Op:    policy.OperatorEq,
				Right: true,
			},
			facts: decision.Facts{factOwned: true},
			want:  true,
		},
		{
			name: "eq on a non-matching boolean",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factOwned},
				Op:    policy.OperatorEq,
				Right: true,
			},
			facts: decision.Facts{factOwned: false},
			want:  false,
		},
		{
			name: "neq inverts eq",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorNeq,
				Right: choiceRead,
			},
			facts: decision.Facts{factKind: choiceWrite},
			want:  true,
		},
		{
			name: "lt is strict",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorLt,
				Right: 5,
			},
			facts: decision.Facts{factCount: float64(5)},
			want:  false,
		},
		{
			name: "lte includes the boundary",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorLte,
				Right: 5,
			},
			facts: decision.Facts{factCount: float64(5)},
			want:  true,
		},
		{
			name: "gt is strict",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorGt,
				Right: 5,
			},
			facts: decision.Facts{factCount: float64(5)},
			want:  false,
		},
		{
			name: "gte includes the boundary",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorGte,
				Right: 5,
			},
			facts: decision.Facts{factCount: float64(5)},
			want:  true,
		},
		{
			name: "an integer fact compares against a float threshold",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factCount},
				Op:    policy.OperatorGte,
				Right: 2.5,
			},
			facts: decision.Facts{factCount: 3},
			want:  true,
		},
		{
			name: "in finds a member",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorIn,
				Right: []any{choiceRead, choiceWrite},
			},
			facts: decision.Facts{factKind: choiceWrite},
			want:  true,
		},
		{
			name: "in rejects a non-member",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factKind},
				Op:    policy.OperatorIn,
				Right: []any{choiceRead, choiceWrite},
			},
			facts: decision.Facts{factKind: "delete"},
			want:  false,
		},
		{
			name: "exists is true for a supplied optional fact",
			condition: &policy.Condition{
				Left: &policy.Operand{Fact: factPath},
				Op:   policy.OperatorExists,
			},
			facts: decision.Facts{factPath: testFactPathTemp},
			want:  true,
		},
		{
			name: "exists is false for an absent optional fact",
			condition: &policy.Condition{
				Left: &policy.Operand{Fact: factPath},
				Op:   policy.OperatorExists,
			},
			facts: decision.Facts{},
			want:  false,
		},
		{
			name: "exists false matches an absent optional fact",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factPath},
				Op:    policy.OperatorExists,
				Right: false,
			},
			facts: decision.Facts{},
			want:  true,
		},
		{
			name: "a non-exists operator on an absent optional fact is false",
			condition: &policy.Condition{
				Left:  &policy.Operand{Fact: factPath},
				Op:    policy.OperatorEq,
				Right: testFactPathTemp,
			},
			facts: decision.Facts{},
			want:  false,
		},
		{
			name: "all needs every branch",
			condition: &policy.Condition{All: []policy.Condition{
				{
					Left:  &policy.Operand{Fact: factOwned},
					Op:    policy.OperatorEq,
					Right: true,
				},
				{
					Left:  &policy.Operand{Fact: factKind},
					Op:    policy.OperatorEq,
					Right: choiceRead,
				},
			}},
			facts: decision.Facts{factOwned: true, factKind: choiceWrite},
			want:  false,
		},
		{
			name: "any needs one branch",
			condition: &policy.Condition{Any: []policy.Condition{
				{
					Left:  &policy.Operand{Fact: factOwned},
					Op:    policy.OperatorEq,
					Right: false,
				},
				{
					Left:  &policy.Operand{Fact: factKind},
					Op:    policy.OperatorEq,
					Right: choiceWrite,
				},
			}},
			facts: decision.Facts{factOwned: true, factKind: choiceWrite},
			want:  true,
		},
		{
			name: "not inverts",
			condition: &policy.Condition{Not: &policy.Condition{
				Left:  &policy.Operand{Fact: factOwned},
				Op:    policy.OperatorEq,
				Right: true,
			}},
			facts: decision.Facts{factOwned: true},
			want:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compiled, err := policy.Compile(withPreCondition(tc.condition))
			require.NoError(t, err)

			require.Len(t, compiled.PreRules, 1)
			assert.Equal(
				t, tc.want, compiled.PreRules[0].Matches(tc.facts, nil),
			)
		})
	}
}

func TestConditionEvaluationOverAnswers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		condition *policy.Condition
		answers   decision.Answers
		want      bool
	}{
		{
			name: "choice equals the selected option",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionClass,
					Field:  decision.AnswerFieldChoice,
				},
				Op:    policy.OperatorEq,
				Right: choiceRead,
			},
			answers: validAnswers(),
			want:    true,
		},
		{
			name: "confidence below a threshold",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionClass,
					Field:  decision.AnswerFieldConfidence,
				},
				Op:    policy.OperatorLt,
				Right: 0.9,
			},
			answers: validAnswers(),
			want:    true,
		},
		{
			name: "choice option probability can match without being selected",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer:         questionClass,
					Field:          decision.AnswerFieldProbability,
					ProbabilityKey: choiceWrite,
				},
				Op:    policy.OperatorGte,
				Right: 0.15,
			},
			answers: validAnswers(),
			want:    true,
		},
		{
			name: "score level probability can match",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer:         questionSpread,
					Field:          decision.AnswerFieldProbability,
					ProbabilityKey: "1",
				},
				Op:    policy.OperatorGte,
				Right: 0.5,
			},
			answers: validAnswers(),
			want:    true,
		},
		{
			name: "score at the threshold with gte",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionSpread,
					Field:  decision.AnswerFieldScore,
				},
				Op:    policy.OperatorGte,
				Right: 0.5,
			},
			answers: validAnswers(),
			want:    true,
		},
		{
			name: "noul below a threshold",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionRisky,
					Field:  decision.AnswerFieldNoul,
				},
				Op:    policy.OperatorLt,
				Right: 0.35,
			},
			answers: validAnswers(),
			want:    true,
		},
		{
			name: "a missing answer makes the leaf false",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionRisky,
					Field:  decision.AnswerFieldNoul,
				},
				Op:    policy.OperatorLt,
				Right: 0.35,
			},
			answers: decision.Answers{},
			want:    false,
		},
		{
			name: "an answer without confidence makes the leaf false",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer: questionClass,
					Field:  decision.AnswerFieldConfidence,
				},
				Op:    policy.OperatorLt,
				Right: 0.9,
			},
			answers: decision.Answers{
				questionClass: {
					Type:   decision.QuestionTypeChoice,
					Choice: choiceRead,
				},
			},
			want: false,
		},
		{
			name: "missing probability makes the leaf false",
			condition: &policy.Condition{
				Left: &policy.Operand{
					Answer:         questionClass,
					Field:          decision.AnswerFieldProbability,
					ProbabilityKey: choiceWrite,
				},
				Op:    policy.OperatorGte,
				Right: 0.15,
			},
			answers: decision.Answers{
				questionClass: {
					Type:          decision.QuestionTypeChoice,
					Choice:        choiceRead,
					Confidence:    0.9,
					HasConfidence: true,
					Probabilities: map[string]float64{choiceRead: 1},
				},
			},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compiled, err := policy.Compile(withDecisionCondition(tc.condition))
			require.NoError(t, err)

			require.Len(t, compiled.DecisionRules, 1)
			assert.Equal(
				t,
				tc.want,
				compiled.DecisionRules[0].Matches(validFacts(), tc.answers),
			)
		})
	}
}
