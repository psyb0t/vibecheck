package policy_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const exampleDir = "../../../examples/agent-action-firewall"

const supportTicketExample = "../../../examples/support-ticket-router/" +
	"policy.yaml"

// acceptanceFile is the shipped vector set. It is deliberately data, not Go,
// so the same file documents the policy's behavior for a reader and drives
// this test.
type acceptanceFile struct {
	Policy  policy.Ref         `yaml:"policy"`
	Vectors []acceptanceVector `yaml:"vectors"`
}

type acceptanceVector struct {
	Name        string                    `yaml:"name"`
	Description string                    `yaml:"description"`
	Facts       decision.Facts            `yaml:"facts"`
	Answers     map[string]scriptedAnswer `yaml:"answers"`
	Expect      acceptanceExpectation     `yaml:"expect"`
}

// scriptedAnswer is the readable shorthand a vector uses. The loader expands
// it into a full decision.Answer and only synthesizes a distribution when the
// vector does not need exact probability values.
type scriptedAnswer struct {
	Noul          *float64           `yaml:"noul"`
	Score         *float64           `yaml:"score"`
	Choice        string             `yaml:"choice"`
	Confidence    *float64           `yaml:"confidence"`
	Probabilities map[string]float64 `yaml:"probabilities"`
}

type acceptanceExpectation struct {
	Outcome  decision.Outcome       `yaml:"outcome"`
	Rule     string                 `yaml:"rule"`
	Path     decision.ExecutionPath `yaml:"path"`
	Rejected bool                   `yaml:"rejected"`
}

func TestShippedExamplePolicyCompiles(t *testing.T) {
	t.Parallel()

	compiled := loadExamplePolicy(t)

	assert.Equal(
		t,
		policy.Ref{Name: "agent-action-firewall", Version: testPolicyVersion},
		compiled.Ref,
	)
	assert.Equal(t, outcomeReview, compiled.DefaultOutcome)
	assert.Equal(
		t,
		[]string{"actionClass", "blastRadius", "destructive"},
		compiled.QuestionIDs,
	)
	assert.Len(t, compiled.PreRules, 1)
	assert.Len(t, compiled.DecisionRules, 4)
}

func TestSupportTicketRouterCompiles(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(supportTicketExample)
	require.NoError(t, err)

	compiled, err := policy.CompileBytes(data, 0)
	require.NoError(t, err)

	assert.Equal(
		t,
		policy.Ref{Name: "support-ticket-router", Version: testPolicyVersion},
		compiled.Ref,
	)
	assert.Equal(t, decision.Outcome("other"), compiled.DefaultOutcome)
	assert.Equal(t, []string{"destination"}, compiled.QuestionIDs)
	assert.Empty(t, compiled.RequiredFacts)
	assert.Empty(t, compiled.PreRules)
}

func TestSupportTicketRouterMapsProviderChoices(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(supportTicketExample)
	require.NoError(t, err)

	compiled, err := policy.CompileBytes(data, 0)
	require.NoError(t, err)

	testCases := []struct {
		name        string
		choice      string
		wantOutcome decision.Outcome
		wantRule    string
	}{
		{
			name:        "billing",
			choice:      "billing",
			wantOutcome: "billing",
			wantRule:    "route-billing",
		},
		{
			name:        "technical",
			choice:      "technical",
			wantOutcome: "technical",
			wantRule:    "route-technical",
		},
		{name: "other", choice: "other", wantOutcome: "other"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers := decision.Answers{
				"destination": {
					Type:          decision.QuestionTypeChoice,
					Choice:        tc.choice,
					Confidence:    0.8,
					HasConfidence: true,
					Probabilities: map[string]float64{
						"billing":   0.1,
						"technical": 0.1,
						"other":     0.1,
						tc.choice:   0.8,
					},
				},
			}

			result := compiled.EvaluateDecisionRules(nil, answers)

			assert.Equal(t, tc.wantOutcome, result.Outcome)
			assert.Equal(t, tc.wantRule, result.MatchedRuleID)
		})
	}
}

func TestShippedExamplePolicyAcceptanceVectors(t *testing.T) {
	t.Parallel()

	compiled := loadExamplePolicy(t)
	fixture := loadAcceptanceFile(t)

	require.Equal(
		t, compiled.Ref, fixture.Policy,
		"the vector file must name the policy it exercises",
	)
	require.NotEmpty(t, fixture.Vectors)

	for _, vector := range fixture.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			t.Parallel()

			err := compiled.ValidateFacts(vector.Facts)

			if vector.Expect.Rejected {
				require.ErrorIs(
					t, err, commerr.ErrValidationFailed,
					"a vector marked rejected must fail input validation",
				)

				return
			}

			require.NoError(t, err)

			result, matched := compiled.EvaluatePreRules(vector.Facts)
			if vector.Expect.Path == decision.ExecutionPathPreRule {
				require.True(
					t, matched,
					"this vector expects a deterministic short-circuit with no provider call", //nolint:lll // one string literal; splitting it would change the text
				)
				require.Empty(
					t, vector.Answers,
					"a pre-rule vector must not script answers, since none are ever requested", //nolint:lll // one string literal; splitting it would change the text
				)
				assertResult(t, vector, result)

				return
			}

			require.False(
				t, matched,
				"this vector expects the provider to be consulted",
			)

			answers := expandAnswers(t, compiled, vector.Answers)
			require.NoError(
				t, compiled.ValidateAnswers(answers),
				"a vector's scripted answers must satisfy the provider contract", //nolint:lll // one string literal; splitting it would change the text
			)

			assertResult(
				t,
				vector,
				compiled.EvaluateDecisionRules(vector.Facts, answers),
			)
		})
	}
}

func assertResult(t *testing.T, vector acceptanceVector, result policy.Result) {
	t.Helper()

	assert.Equal(t, vector.Expect.Outcome, result.Outcome)
	assert.Equal(t, vector.Expect.Rule, result.MatchedRuleID)
	assert.Equal(t, vector.Expect.Path, result.Path)
}

func loadExamplePolicy(t *testing.T) *policy.Compiled {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(exampleDir, "policy.yaml"))
	require.NoError(t, err)

	compiled, err := policy.CompileBytes(data, 0)
	require.NoError(
		t,
		err,
		"the shipped example must compile with the shipped compiler",
	)

	return compiled
}

func loadAcceptanceFile(t *testing.T) acceptanceFile {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(exampleDir, "acceptance.yaml"))
	require.NoError(t, err)

	fixture := acceptanceFile{}
	require.NoError(t, yaml.Unmarshal(data, &fixture))

	return fixture
}

// expandAnswers turns each vector's shorthand into a full answer, synthesizing
// a distribution that puts the stated confidence on the selected option or
// level and spreads the remainder evenly over the others.
func expandAnswers(
	t *testing.T,
	compiled *policy.Compiled,
	scripted map[string]scriptedAnswer,
) decision.Answers {
	t.Helper()

	require.Len(
		t, scripted, len(compiled.QuestionIDs),
		"a vector must script exactly the questions the policy declares",
	)

	answers := make(decision.Answers, len(scripted))

	for _, id := range compiled.QuestionIDs {
		script, ok := scripted[id]
		require.True(t, ok, "vector is missing an answer for question %q", id)

		question := compiled.Questions[id]

		switch question.Type {
		case decision.QuestionTypeNoul:
			require.NotNil(t, script.Noul, "question %q needs a noul", id)
			answers[id] = decision.Answer{
				Type: decision.QuestionTypeNoul,
				Noul: *script.Noul,
			}

		case decision.QuestionTypeChoice:
			require.NotEmpty(t, script.Choice, "question %q needs a choice", id)
			require.NotNil(
				t,
				script.Confidence,
				"question %q needs a confidence",
				id,
			)
			answers[id] = decision.Answer{
				Type:          decision.QuestionTypeChoice,
				Choice:        script.Choice,
				Confidence:    *script.Confidence,
				HasConfidence: true,
				Probabilities: scriptedDistribution(
					script.Probabilities,
					question.ChoiceKeys,
					script.Choice,
				),
			}

		case decision.QuestionTypeScore:
			require.NotNil(t, script.Score, "question %q needs a score", id)
			require.NotNil(
				t,
				script.Confidence,
				"question %q needs a confidence",
				id,
			)
			answers[id] = decision.Answer{
				Type:          decision.QuestionTypeScore,
				Score:         *script.Score,
				Confidence:    *script.Confidence,
				HasConfidence: true,
				Probabilities: scriptedDistribution(
					script.Probabilities,
					levelKeys(question),
					nearestLevel(question, *script.Score),
				),
				Legend: legendFor(question),
			}
		}
	}

	return answers
}

func scriptedDistribution(
	provided map[string]float64,
	keys []string,
	selected string,
) map[string]float64 {
	if provided != nil {
		return provided
	}

	return spreadOver(keys, selected)
}

// spreadOver builds a distribution summing to exactly 1 with most of the mass
// on selected.
func spreadOver(keys []string, selected string) map[string]float64 {
	const selectedMass = 0.8

	remainder := 1.0 - selectedMass
	others := len(keys) - 1

	distribution := make(map[string]float64, len(keys))

	for _, key := range keys {
		if key == selected {
			distribution[key] = selectedMass

			continue
		}

		distribution[key] = remainder / float64(others)
	}

	return distribution
}

func levelKeys(question policy.CompiledQuestion) []string {
	keys := make([]string, 0, len(question.ScoreCriteria))
	for index := range question.ScoreCriteria {
		keys = append(keys, strconv.Itoa(index))
	}

	return keys
}

func nearestLevel(question policy.CompiledQuestion, score float64) string {
	index := min(max(int(score+0.5), 0), len(question.ScoreCriteria)-1)

	return strconv.Itoa(index)
}

func legendFor(question policy.CompiledQuestion) map[string]any {
	legend := make(map[string]any, len(question.ScoreCriteria))
	for index, text := range question.ScoreCriteria {
		legend[strconv.Itoa(index)] = text
	}

	return legend
}
