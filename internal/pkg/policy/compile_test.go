package policy_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileAcceptsAValidDocument(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	assert.Equal(
		t,
		policy.Ref{Name: "unit-policy", Version: testPolicyVersion},
		compiled.Ref,
	)
	assert.Equal(t, outcomeReview, compiled.DefaultOutcome)
	assert.True(t, compiled.HasOutcome(outcomeBlock))
	assert.False(t, compiled.HasOutcome(decision.Outcome("escalate")))

	assert.Equal(
		t,
		[]string{questionClass, questionRisky, questionSpread},
		compiled.QuestionIDs,
		"question IDs stay sorted so the request and hash are stable",
	)

	require.Len(t, compiled.PreRules, 1)
	assert.Equal(t, rulePreBlockUnowned, compiled.PreRules[0].ID)
	require.Len(t, compiled.DecisionRules, 2)
	assert.Equal(t, ruleReviewUnknown, compiled.DecisionRules[0].ID)
	assert.Equal(t, ruleAllowLowRisk, compiled.DecisionRules[1].ID)

	assert.NotEmpty(t, compiled.Hash)
	assert.NotEmpty(t, compiled.Canonical)
	assert.True(t, compiled.NeedsProvider())
}

func TestCompileResolvesQuestionCriteriaPerType(t *testing.T) {
	t.Parallel()

	compiled := compileValid(t)

	noul := compiled.Questions[questionRisky]
	assert.Equal(t, decision.QuestionTypeNoul, noul.Type)
	assert.Empty(t, noul.ChoiceCriteria)
	assert.Empty(t, noul.ScoreCriteria)

	score := compiled.Questions[questionSpread]
	assert.Equal(
		t,
		[]string{scoreLevelDisposable, scoreLevelProject, scoreLevelUnrelated},
		score.ScoreCriteria,
		"score levels keep document order: the index is the level number",
	)

	choice := compiled.Questions[questionClass]
	assert.Equal(
		t,
		[]string{choiceRead, choiceUnknown, choiceWrite},
		choice.ChoiceKeys,
		"choice keys are sorted for determinism",
	)
	assert.Equal(
		t, "", choice.ChoiceCriteria[choiceUnknown],
		"a null option description is allowed and becomes empty",
	)
}

func TestCompileRejectsSchemaIdentity(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		doc     *policy.Document
		wantErr error
	}{
		{
			name:    "nil document",
			doc:     nil,
			wantErr: policy.ErrInvalidDocument,
		},
		{
			name: "wrong api version",
			doc: mutate(func(d *policy.Document) {
				d.APIVersion = "other/v1"
			}),
			wantErr: policy.ErrUnsupportedAPIVersion,
		},
		{
			name:    "empty api version",
			doc:     mutate(func(d *policy.Document) { d.APIVersion = "" }),
			wantErr: policy.ErrUnsupportedAPIVersion,
		},
		{
			name:    "wrong kind",
			doc:     mutate(func(d *policy.Document) { d.Kind = "ConfigMap" }),
			wantErr: policy.ErrUnsupportedKind,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestCompileRejectsBadMetadata(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"empty name",
			mutate(func(d *policy.Document) { d.Metadata.Name = "" }),
		},
		{
			"name with a space",
			mutate(func(d *policy.Document) { d.Metadata.Name = "my policy" }),
		},
		{
			"name starting with a digit",
			mutate(func(d *policy.Document) { d.Metadata.Name = "1policy" }),
		},
		{
			"name starting with a hyphen",
			mutate(func(d *policy.Document) { d.Metadata.Name = "-policy" }),
		},
		{
			"name with a dot",
			mutate(func(d *policy.Document) { d.Metadata.Name = "my.policy" }),
		},
		{
			"unicode name",
			mutate(func(d *policy.Document) { d.Metadata.Name = "poliçy" }),
		},
		{
			"name one over the ceiling",
			mutate(func(d *policy.Document) {
				d.Metadata.Name = longName(decision.MaxIdentifierLength + 1)
			}),
		},
		{
			"empty version",
			mutate(func(d *policy.Document) { d.Metadata.Version = "" }),
		},
		{
			"padded version",
			mutate(func(d *policy.Document) { d.Metadata.Version = " 1.0.0 " }),
		},
		{
			"version one over the ceiling",
			mutate(func(d *policy.Document) {
				d.Metadata.Version = strings.Repeat(
					"9", policy.MaxVersionLength+1,
				)
			}),
		},
		{
			"description one over the ceiling",
			mutate(func(d *policy.Document) {
				d.Metadata.Description = strings.Repeat(
					"x", policy.MaxDescriptionLength+1,
				)
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.ErrorIs(t, err, policy.ErrInvalidMetadata)
		})
	}
}

func TestCompileAcceptsIdentifiersAtTheCeiling(t *testing.T) {
	t.Parallel()

	doc := mutate(func(d *policy.Document) {
		d.Metadata.Name = longName(decision.MaxIdentifierLength)
		d.Metadata.Version = strings.Repeat("9", policy.MaxVersionLength)
		d.Metadata.Description = strings.Repeat(
			"x", policy.MaxDescriptionLength,
		)
	})

	_, err := policy.Compile(doc)
	require.NoError(t, err)
}

func TestCompileRejectsBadOutcomes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"no outcomes",
			mutate(func(d *policy.Document) { d.Spec.Outcomes = nil }),
		},
		{
			"malformed outcome",
			mutate(func(d *policy.Document) {
				d.Spec.Outcomes = []decision.Outcome{
					"allow", "not ok", "review",
				}
			}),
		},
		{
			"duplicate outcome",
			mutate(func(d *policy.Document) {
				d.Spec.Outcomes = []decision.Outcome{
					outcomeAllow, outcomeAllow, outcomeReview,
				}
			}),
		},
		{
			"default not declared",
			mutate(func(d *policy.Document) {
				d.Spec.DefaultOutcome = "escalate"
			}),
		},
		{
			"empty default",
			mutate(func(d *policy.Document) { d.Spec.DefaultOutcome = "" }),
		},
		{
			"too many outcomes",
			mutate(func(d *policy.Document) {
				outcomes := make(
					[]decision.Outcome, 0, policy.MaxOutcomes+1,
				)
				for i := range policy.MaxOutcomes + 1 {
					outcomes = append(
						outcomes,
						decision.Outcome("o"+strconv.Itoa(i)),
					)
				}

				d.Spec.Outcomes = outcomes
				d.Spec.DefaultOutcome = outcomes[0]
				d.Spec.PreRules = nil
				d.Spec.DecisionRules = nil
			}),
		},
		{
			"rule selects an undeclared outcome",
			mutate(func(d *policy.Document) {
				d.Spec.PreRules[0].Outcome = "escalate"
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.ErrorIs(t, err, policy.ErrInvalidOutcome)
		})
	}
}

func TestCompileRejectsBadFacts(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"malformed fact name",
			mutate(func(d *policy.Document) {
				facts := d.Spec.Input.RequiredFacts
				facts["not a name"] = decision.FactTypeString
			}),
		},
		{
			"fact name with a trailing dot",
			mutate(func(d *policy.Document) {
				d.Spec.Input.RequiredFacts["action."] = decision.FactTypeString
			}),
		},
		{
			"fact name with a double dot",
			mutate(func(d *policy.Document) {
				facts := d.Spec.Input.RequiredFacts
				facts["action..kind"] = decision.FactTypeString
			}),
		},
		{
			"unsupported fact type",
			mutate(func(d *policy.Document) {
				facts := d.Spec.Input.RequiredFacts
				facts["action.payload"] = decision.FactType("object")
			}),
		},
		{
			"fact declared both required and optional",
			mutate(func(d *policy.Document) {
				d.Spec.Input.OptionalFacts[factKind] = decision.FactTypeString
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.ErrorIs(t, err, policy.ErrInvalidFact)
		})
	}
}

func TestCompileRejectsBadQuestions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"no questions",
			mutate(func(d *policy.Document) { d.Spec.Questions = nil }),
		},
		{
			"malformed question id",
			mutate(func(d *policy.Document) {
				d.Spec.Questions["bad id"] = policy.Question{
					Type:         decision.QuestionTypeNoul,
					Instructions: "ok",
				}
			}),
		},
		{
			"unsupported question type",
			mutate(func(d *policy.Document) {
				d.Spec.Questions["extra"] = policy.Question{
					Type:         decision.QuestionType("ranking"),
					Instructions: "ok",
				}
			}),
		},
		{
			"blank instructions",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionRisky]
				question.Instructions = "   "
				d.Spec.Questions[questionRisky] = question
			}),
		},
		{
			"instructions one over the ceiling",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionRisky]
				question.Instructions = strings.Repeat(
					"x", policy.MaxInstructionsLength+1,
				)
				d.Spec.Questions[questionRisky] = question
			}),
		},
		{
			"noul with criteria",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionRisky]
				question.Criteria = []any{"yes", "no"}
				d.Spec.Questions[questionRisky] = question
			}),
		},
		{
			"choice criteria as a list",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = []any{choiceRead, choiceWrite}
				d.Spec.Questions[questionClass] = question
			}),
		},
		{
			"choice with one option",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = numberedOptions(
					policy.MinChoiceCriteria - 1,
				)
				d.Spec.Questions[questionClass] = question
			}),
		},
		{
			"choice one over the option ceiling",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = numberedOptions(
					policy.MaxChoiceCriteria + 1,
				)
				d.Spec.Questions[questionClass] = question
			}),
		},
		{
			"choice option key not an identifier",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = map[string]any{
					choiceRead: "r",
					"not ok":   "n",
				}
				d.Spec.Questions[questionClass] = question
			}),
		},
		{
			"choice option description not a string",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = map[string]any{
					choiceRead:  "r",
					choiceWrite: 42,
				}
				d.Spec.Questions[questionClass] = question
			}),
		},
		{
			"score criteria as a map",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = map[string]any{
					testLowCriteria: "l",
					"high":          "h",
				}
				d.Spec.Questions[questionSpread] = question
			}),
		},
		{
			"score with one level",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = numberedLevels(policy.MinScoreCriteria - 1)
				d.Spec.Questions[questionSpread] = question
			}),
		},
		{
			"score one over the level ceiling",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = numberedLevels(policy.MaxScoreCriteria + 1)
				d.Spec.Questions[questionSpread] = question
			}),
		},
		{
			"score level not a string",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = []any{testLowCriteria, 7}
				d.Spec.Questions[questionSpread] = question
			}),
		},
		{
			"score level blank",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = []any{testLowCriteria, "  "}
				d.Spec.Questions[questionSpread] = question
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.ErrorIs(t, err, policy.ErrInvalidQuestion)
		})
	}
}

func TestCompileAcceptsCriteriaAtTheBoundaries(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"choice at the minimum",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = numberedOptions(policy.MinChoiceCriteria)
				d.Spec.Questions[questionClass] = question
				d.Spec.DecisionRules = nil
			}),
		},
		{
			"choice at the maximum",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionClass]
				question.Criteria = numberedOptions(policy.MaxChoiceCriteria)
				d.Spec.Questions[questionClass] = question
				d.Spec.DecisionRules = nil
			}),
		},
		{
			"score at the minimum",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = numberedLevels(policy.MinScoreCriteria)
				d.Spec.Questions[questionSpread] = question
			}),
		},
		{
			"score at the maximum",
			mutate(func(d *policy.Document) {
				question := d.Spec.Questions[questionSpread]
				question.Criteria = numberedLevels(policy.MaxScoreCriteria)
				d.Spec.Questions[questionSpread] = question
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.NoError(t, err)
		})
	}
}

func TestCompileRejectsBadRules(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		doc  *policy.Document
	}{
		{
			"malformed rule id",
			mutate(func(d *policy.Document) {
				d.Spec.PreRules[0].ID = "not an id"
			}),
		},
		{
			"empty rule id",
			mutate(func(d *policy.Document) { d.Spec.PreRules[0].ID = "" }),
		},
		{
			"duplicate rule id within a phase",
			mutate(func(d *policy.Document) {
				d.Spec.DecisionRules[1].ID = d.Spec.DecisionRules[0].ID
			}),
		},
		{
			"duplicate rule id across phases",
			mutate(func(d *policy.Document) {
				d.Spec.DecisionRules[0].ID = d.Spec.PreRules[0].ID
			}),
		},
		{
			"rule description one over the ceiling",
			mutate(func(d *policy.Document) {
				d.Spec.PreRules[0].Description = strings.Repeat(
					"x", policy.MaxDescriptionLength+1,
				)
			}),
		},
		{
			"rule after an unconditional rule is unreachable",
			mutate(func(d *policy.Document) {
				d.Spec.DecisionRules[0].When = nil
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := policy.Compile(tc.doc)
			require.ErrorIs(t, err, policy.ErrInvalidRule)
		})
	}
}

func TestCompileAcceptsATrailingUnconditionalRule(t *testing.T) {
	t.Parallel()

	doc := mutate(func(d *policy.Document) {
		d.Spec.DecisionRules[1].When = nil
	})

	compiled, err := policy.Compile(doc)
	require.NoError(t, err)

	require.Len(t, compiled.DecisionRules, 2)
	assert.Nil(t, compiled.DecisionRules[1].Condition)
	assert.True(
		t,
		compiled.DecisionRules[1].Matches(validFacts(), validAnswers()),
		"an unconditional rule always matches",
	)
}

func TestCompileAcceptsAPolicyWithNoRules(t *testing.T) {
	t.Parallel()

	doc := mutate(func(d *policy.Document) {
		d.Spec.PreRules = nil
		d.Spec.DecisionRules = nil
	})

	compiled, err := policy.Compile(doc)
	require.NoError(t, err)

	_, matched := compiled.EvaluatePreRules(validFacts())
	assert.False(t, matched)

	result := compiled.EvaluateDecisionRules(validFacts(), validAnswers())
	assert.Equal(t, outcomeReview, result.Outcome)
	assert.Equal(t, decision.ExecutionPathDefault, result.Path)
}

func TestCompileWithMaxQuestionsEnforcesDeploymentCeiling(t *testing.T) {
	t.Parallel()

	doc := validDocument()

	_, err := policy.CompileWithMaxQuestions(doc, len(doc.Spec.Questions)-1)
	require.ErrorIs(t, err, policy.ErrInvalidQuestion)

	compiled, err := policy.CompileWithMaxQuestions(
		doc,
		len(doc.Spec.Questions),
	)
	require.NoError(t, err)
	require.Len(t, compiled.Questions, len(doc.Spec.Questions))
}
