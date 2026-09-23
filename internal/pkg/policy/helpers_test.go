package policy_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/require"
)

const (
	outcomeAllow  decision.Outcome = "allow"
	outcomeReview decision.Outcome = "review"
	outcomeBlock  decision.Outcome = "block"

	questionRisky  = "risky"
	questionSpread = "spread"
	questionClass  = "actionClass"

	factDestructive = "action.destructive"
	factOwned       = "target.ownedBySession"
	factKind        = "action.kind"
	factCount       = "action.count"
	factPath        = "target.path"

	choiceRead    = "read"
	choiceWrite   = "write"
	choiceUnknown = "unknown"

	rulePreBlockUnowned     = "block-unowned-destructive"
	ruleReviewUnknown       = "review-unknown-class"
	ruleReviewPossibleWrite = "review-possible-write"
	ruleAllowLowRisk        = "allow-owned-low-risk"

	testPolicyVersion = "1.0.0"
	testFactPathTemp  = "/tmp/x"
	testLowCriteria   = "low"

	scoreLevelDisposable = "one disposable item"
	scoreLevelProject    = "the current project"
	scoreLevelUnrelated  = "unrelated user work"

	policyNameAlpha = "alpha"
)

// validDocument returns a document that compiles. Every compile test starts
// from this and breaks exactly one thing, so a failure names the check that
// caught it rather than whichever check happened to run first.
func validDocument() *policy.Document {
	return &policy.Document{
		APIVersion: policy.APIVersion,
		Kind:       policy.Kind,
		Metadata: policy.Metadata{
			Name:        "unit-policy",
			Version:     testPolicyVersion,
			Description: "a policy used by the unit tests",
		},
		Spec: policy.Spec{
			Model: "jev-latest",
			Outcomes: []decision.Outcome{
				outcomeAllow, outcomeReview, outcomeBlock,
			},
			DefaultOutcome: outcomeReview,
			Input: policy.Input{
				RequiredFacts: map[string]decision.FactType{
					factDestructive: decision.FactTypeBoolean,
					factOwned:       decision.FactTypeBoolean,
					factKind:        decision.FactTypeString,
					factCount:       decision.FactTypeNumber,
				},
				OptionalFacts: map[string]decision.FactType{
					factPath: decision.FactTypeString,
				},
			},
			PreRules: []policy.Rule{{
				ID:          rulePreBlockUnowned,
				Description: "the ownership floor stays outside model control",
				When: &policy.Condition{All: []policy.Condition{
					{
						Left:  &policy.Operand{Fact: factDestructive},
						Op:    policy.OperatorEq,
						Right: true,
					},
					{
						Left:  &policy.Operand{Fact: factOwned},
						Op:    policy.OperatorEq,
						Right: false,
					},
				}},
				Outcome: outcomeBlock,
			}},
			Questions: map[string]policy.Question{
				questionRisky: {
					Type:         decision.QuestionTypeNoul,
					Instructions: "Is the action hard to reverse?",
				},
				questionSpread: {
					Type:         decision.QuestionTypeScore,
					Instructions: "How wide is the blast radius?",
					Criteria: []any{
						scoreLevelDisposable,
						scoreLevelProject,
						scoreLevelUnrelated,
					},
				},
				questionClass: {
					Type:         decision.QuestionTypeChoice,
					Instructions: "Which class describes the action?",
					Criteria: map[string]any{
						choiceRead:    "reads state",
						choiceWrite:   "changes state",
						choiceUnknown: nil,
					},
				},
			},
			DecisionRules: []policy.Rule{
				{
					ID: ruleReviewUnknown,
					When: &policy.Condition{
						Left: &policy.Operand{
							Answer: questionClass,
							Field:  decision.AnswerFieldChoice,
						},
						Op:    policy.OperatorEq,
						Right: choiceUnknown,
					},
					Outcome: outcomeReview,
				},
				{
					ID: ruleAllowLowRisk,
					When: &policy.Condition{All: []policy.Condition{
						{
							Left:  &policy.Operand{Fact: factOwned},
							Op:    policy.OperatorEq,
							Right: true,
						},
						{
							Left: &policy.Operand{
								Answer: questionRisky,
								Field:  decision.AnswerFieldNoul,
							},
							Op:    policy.OperatorLt,
							Right: 0.35,
						},
					}},
					Outcome: outcomeAllow,
				},
			},
		},
	}
}

// mutate copies the valid document and applies one change to the copy.
func mutate(change func(doc *policy.Document)) *policy.Document {
	doc := validDocument()
	change(doc)

	return doc
}

// compileValid compiles the unmutated document. Every other case depends on
// that baseline holding, so this fails the test rather than returning an error.
func compileValid(t *testing.T) *policy.Compiled {
	t.Helper()

	compiled, err := policy.Compile(validDocument())
	require.NoError(t, err)

	return compiled
}

// validFacts returns a fact set that satisfies the valid document.
func validFacts() decision.Facts {
	return decision.Facts{
		factDestructive: false,
		factOwned:       true,
		factKind:        choiceRead,
		factCount:       float64(1),
	}
}

// validAnswers returns an answer set that satisfies the valid document's
// questions. Callers adjust individual answers to steer a decision rule.
func validAnswers() decision.Answers {
	return decision.Answers{
		questionRisky: {
			Type: decision.QuestionTypeNoul,
			Noul: 0.1,
		},
		questionSpread: {
			Type:          decision.QuestionTypeScore,
			Score:         0.5,
			Confidence:    0.9,
			HasConfidence: true,
			Probabilities: map[string]float64{"0": 0.5, "1": 0.5, "2": 0.0},
			Legend: map[string]any{
				"0": scoreLevelDisposable,
				"1": scoreLevelProject,
				"2": scoreLevelUnrelated,
			},
		},
		questionClass: {
			Type:          decision.QuestionTypeChoice,
			Choice:        choiceRead,
			Confidence:    0.8,
			HasConfidence: true,
			Probabilities: map[string]float64{
				choiceRead:    0.8,
				choiceWrite:   0.15,
				choiceUnknown: 0.05,
			},
		},
	}
}

// numberedLevels builds n distinct score level descriptions.
func numberedLevels(n int) []any {
	levels := make([]any, 0, n)
	for i := range n {
		levels = append(levels, "level "+strconv.Itoa(i))
	}

	return levels
}

// numberedOptions builds n distinct choice options.
func numberedOptions(n int) map[string]any {
	options := make(map[string]any, n)
	for i := range n {
		options["opt"+strconv.Itoa(i)] = "option " + strconv.Itoa(i)
	}

	return options
}

// factLeaf builds a leaf over the session-ownership boolean, which the nesting
// and fan-out boundary cases reuse as filler.
func factLeaf() policy.Condition {
	return policy.Condition{
		Left:  &policy.Operand{Fact: factOwned},
		Op:    policy.OperatorEq,
		Right: true,
	}
}

// nestedCondition wraps one leaf in the requested number of combinator levels.
// Zero levels is a bare leaf.
func nestedCondition(levels int) *policy.Condition {
	current := factLeaf()
	for range levels {
		current = policy.Condition{All: []policy.Condition{current}}
	}

	return &current
}

// repeatedLeaves builds n copies of the filler leaf.
func repeatedLeaves(n int) []policy.Condition {
	leaves := make([]policy.Condition, 0, n)
	for range n {
		leaves = append(leaves, factLeaf())
	}

	return leaves
}

// longName builds a grammatical identifier of exactly n characters, for the
// length-boundary cases.
func longName(n int) string {
	return "a" + strings.Repeat("b", n-1)
}
