package policy

import (
	"math"
	"reflect"
	"sort"
	"strconv"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// ProbabilitySumTolerance is how far a returned probability distribution may
// drift from 1.0 before Vibecheck treats the response as a protocol failure.
// Providers round their outputs, so an exact comparison would reject healthy
// responses; anything wider than this is a real contract break.
const ProbabilitySumTolerance = 0.02

// floatComparisonSlack absorbs binary floating-point representation error so a
// distribution sitting exactly on the tolerance boundary is accepted. Summing
// 0.8 + 0.15 + 0.03 does not land on 0.98 exactly, and a response must not be
// rejected for that.
const floatComparisonSlack = 1e-9

// Result is the outcome of a completed evaluation together with the reason it
// was selected.
type Result struct {
	Outcome decision.Outcome

	// MatchedRuleID is the rule that selected the outcome, empty when the
	// default outcome applied.
	MatchedRuleID string

	Path decision.ExecutionPath
}

// EvaluatePreRules runs the deterministic phase. The first matching rule wins.
//
// A match means the caller must not call the provider at all: that is the
// whole point of the phase, and it is what keeps a hard safety floor outside
// model control.
func (c *Compiled) EvaluatePreRules(facts decision.Facts) (Result, bool) {
	for _, rule := range c.PreRules {
		if !rule.Matches(facts, nil) {
			continue
		}

		return Result{
			Outcome:       rule.Outcome,
			MatchedRuleID: rule.ID,
			Path:          decision.ExecutionPathPreRule,
		}, true
	}

	return Result{}, false
}

// EvaluateDecisionRules runs the post-provider phase in file order and falls
// back to the declared default outcome when nothing matches.
func (c *Compiled) EvaluateDecisionRules(
	facts decision.Facts,
	answers decision.Answers,
) Result {
	for _, rule := range c.DecisionRules {
		if !rule.Matches(facts, answers) {
			continue
		}

		return Result{
			Outcome:       rule.Outcome,
			MatchedRuleID: rule.ID,
			Path:          decision.ExecutionPathDecisionRule,
		}
	}

	return Result{
		Outcome: c.DefaultOutcome,
		Path:    decision.ExecutionPathDefault,
	}
}

// ValidateAnswers enforces the provider contract before any rule sees an
// answer: the returned question set matches the requested one exactly, each
// answer has the declared type, and every number is finite and in range.
//
// It returns an error wrapping ErrProviderProtocol so a caller can map a
// misbehaving provider to its own stable error code without inspecting the
// message.
func (c *Compiled) ValidateAnswers(answers decision.Answers) error {
	if len(answers) != len(c.QuestionIDs) {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"provider returned %d answers for %d questions",
			len(answers), len(c.QuestionIDs),
		)
	}

	for _, id := range c.QuestionIDs {
		answer, ok := answers[id]
		if !ok {
			return ctxerrors.Wrapf(
				ErrProviderProtocol,
				"provider omitted an answer for question %q", id,
			)
		}

		if err := validateAnswer(c.Questions[id], answer); err != nil {
			return err
		}
	}

	return nil
}

func validateAnswer(question CompiledQuestion, answer decision.Answer) error {
	if answer.Type != question.Type {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q is a %s but the provider answered with a %s",
			question.ID, question.Type, answer.Type,
		)
	}

	switch question.Type {
	case decision.QuestionTypeNoul:
		return validateNoulAnswer(question, answer)
	case decision.QuestionTypeChoice:
		return validateChoiceAnswer(question, answer)
	case decision.QuestionTypeScore:
		return validateScoreAnswer(question, answer)
	}

	return ctxerrors.Wrapf(
		ErrProviderProtocol,
		"question %q has unsupported type %q", question.ID, question.Type,
	)
}

func validateNoulAnswer(
	question CompiledQuestion,
	answer decision.Answer,
) error {
	if !isProbability(answer.Noul) {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned noul %v which is not a probability",
			question.ID, answer.Noul,
		)
	}

	return nil
}

func validateChoiceAnswer(
	question CompiledQuestion,
	answer decision.Answer,
) error {
	if _, declared := question.ChoiceCriteria[answer.Choice]; !declared {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned option %q which it does not declare",
			question.ID, answer.Choice,
		)
	}

	if err := validateConfidence(question, answer); err != nil {
		return err
	}

	if len(answer.Probabilities) != len(question.ChoiceKeys) {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned %d probabilities for %d options",
			question.ID, len(answer.Probabilities), len(question.ChoiceKeys),
		)
	}

	for _, key := range question.ChoiceKeys {
		if _, ok := answer.Probabilities[key]; !ok {
			return ctxerrors.Wrapf(
				ErrProviderProtocol,
				"question %q returned no probability for option %q",
				question.ID, key,
			)
		}
	}

	return validateDistribution(question.ID, answer.Probabilities)
}

func validateScoreAnswer(
	question CompiledQuestion,
	answer decision.Answer,
) error {
	levels := len(question.ScoreCriteria)

	if err := validateScoreRange(question, answer, levels); err != nil {
		return err
	}

	if err := validateConfidence(question, answer); err != nil {
		return err
	}

	if err := validateScoreCounts(question, answer, levels); err != nil {
		return err
	}

	if err := validateScoreLevelEntries(question, answer); err != nil {
		return err
	}

	return validateDistribution(question.ID, answer.Probabilities)
}

// validateScoreRange rejects a score outside the question's declared level
// range, including non-finite values a provider must never return.
func validateScoreRange(
	question CompiledQuestion,
	answer decision.Answer,
	levels int,
) error {
	maxScore := float64(levels - 1)
	if math.IsNaN(answer.Score) || math.IsInf(answer.Score, 0) ||
		answer.Score < 0 || answer.Score > maxScore {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned score %v outside 0 to %v",
			question.ID, answer.Score, maxScore,
		)
	}

	return nil
}

// validateScoreCounts checks the probability and legend maps each carry one
// entry per declared level.
func validateScoreCounts(
	question CompiledQuestion,
	answer decision.Answer,
	levels int,
) error {
	if len(answer.Probabilities) != levels {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned %d probabilities for %d levels",
			question.ID, len(answer.Probabilities), levels,
		)
	}

	if len(answer.Legend) != levels {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned a %d entry legend for %d levels",
			question.ID, len(answer.Legend), levels,
		)
	}

	return nil
}

// validateScoreLevelEntries checks that every declared level index has both a
// probability and a legend entry.
func validateScoreLevelEntries(
	question CompiledQuestion,
	answer decision.Answer,
) error {
	for index := range question.ScoreCriteria {
		key := strconv.Itoa(index)

		if _, ok := answer.Probabilities[key]; !ok {
			return ctxerrors.Wrapf(
				ErrProviderProtocol,
				"question %q returned no probability for level %s",
				question.ID, key,
			)
		}

		legend, ok := answer.Legend[key]
		if !ok {
			return ctxerrors.Wrapf(
				ErrProviderProtocol,
				"question %q returned no legend entry for level %s",
				question.ID, key,
			)
		}

		if !reflect.DeepEqual(legend, question.ScoreCriteria[index]) {
			return ctxerrors.Wrapf(
				ErrProviderProtocol,
				"question %q returned the wrong legend for level %s",
				question.ID, key,
			)
		}
	}

	return nil
}

func validateConfidence(
	question CompiledQuestion,
	answer decision.Answer,
) error {
	if !answer.HasConfidence {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned no confidence", question.ID,
		)
	}

	if !isProbability(answer.Confidence) {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q returned confidence %v which is not a probability",
			question.ID, answer.Confidence,
		)
	}

	return nil
}

func validateDistribution(
	questionID string,
	probabilities map[string]float64,
) error {
	keys := make([]string, 0, len(probabilities))
	for key := range probabilities {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	total := 0.0

	for _, key := range keys {
		value := probabilities[key]
		if !isProbability(value) {
			return ctxerrors.Wrapf(
				ErrProviderProtocol,
				"question %q returned probability %v for %q outside 0 to 1",
				questionID, value, key,
			)
		}

		total += value
	}

	if math.Abs(total-1.0) > ProbabilitySumTolerance+floatComparisonSlack {
		return ctxerrors.Wrapf(
			ErrProviderProtocol,
			"question %q probabilities sum to %v, outside 1.0 by more than %v",
			questionID, total, ProbabilitySumTolerance,
		)
	}

	return nil
}

func isProbability(value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}

	return value >= minProbability && value <= maxProbability
}
