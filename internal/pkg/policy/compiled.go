package policy

import (
	"sort"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// Ref identifies a policy. The pair is the identity; two documents sharing it
// are the same policy regardless of content, which is exactly why the hash is
// recorded separately on every evaluation.
type Ref struct {
	Name    string `json:"name"    yaml:"name"`
	Version string `json:"version" yaml:"version"`
}

func (r Ref) String() string {
	return r.Name + "@" + r.Version
}

// CompiledQuestion is one question with its criteria resolved into the shape
// its type actually uses.
type CompiledQuestion struct {
	ID           string
	Type         decision.QuestionType
	Instructions any

	// NoulCriteria optionally describes the true and false outcomes. Noul only.
	NoulCriteria map[string]any

	// ChoiceCriteria maps option key to its rubric description, and
	// ChoiceKeys is those keys in sorted order so the provider request and
	// the canonical hash are both deterministic. Choice only.
	ChoiceCriteria map[string]any
	ChoiceKeys     []string

	// ScoreCriteria is the ordered low-to-high level descriptions. Its index
	// is the level number. Score only.
	ScoreCriteria []any
}

// CompiledRule is one type-checked rule. A nil Condition means the rule is
// unconditional and always matches.
type CompiledRule struct {
	ID          string
	Description string
	Condition   *CompiledCondition
	Outcome     decision.Outcome
}

// Matches reports whether this rule fires for the supplied inputs.
func (r CompiledRule) Matches(
	facts decision.Facts,
	answers decision.Answers,
) bool {
	if r.Condition == nil {
		return true
	}

	return r.Condition.Evaluate(facts, answers)
}

// Compiled is a policy that passed every semantic check and is safe to
// evaluate. It is immutable after Compile returns; nothing mutates it.
type Compiled struct {
	Ref         Ref
	Description string
	Model       string

	Outcomes       []decision.Outcome
	DefaultOutcome decision.Outcome
	outcomeSet     map[decision.Outcome]struct{}

	RequiredFacts map[string]decision.FactType
	OptionalFacts map[string]decision.FactType
	StrictInput   bool

	Questions   map[string]CompiledQuestion
	QuestionIDs []string

	PreRules      []CompiledRule
	DecisionRules []CompiledRule

	// Canonical is the deterministic JSON encoding of everything above, and
	// Hash is its lowercase hex SHA-256. Reformatting a document without
	// changing its meaning leaves both untouched; changing any declared
	// behavior changes both.
	Canonical []byte
	Hash      string
}

// HasOutcome reports whether the policy declares this outcome.
func (c *Compiled) HasOutcome(outcome decision.Outcome) bool {
	_, ok := c.outcomeSet[outcome]

	return ok
}

// NeedsProvider reports whether evaluating this policy can require a provider
// call. A policy always declares at least one question, so this is only false
// once a pre-rule has already short-circuited.
func (c *Compiled) NeedsProvider() bool {
	return len(c.QuestionIDs) > 0
}

// ValidateFacts checks caller-supplied facts against the policy's input
// contract: every required fact present and correctly typed, every supplied
// optional fact correctly typed, and no undeclared facts at all when the
// policy asked for strict input.
//
// It returns an error wrapping commerr.ErrValidationFailed, because this is
// bad request data rather than a broken policy.
func (c *Compiled) ValidateFacts(facts decision.Facts) error {
	if err := c.validateRequiredFacts(facts); err != nil {
		return err
	}

	if err := c.validateOptionalFacts(facts); err != nil {
		return err
	}

	if !c.StrictInput {
		return nil
	}

	return c.validateNoUndeclaredFacts(facts)
}

// validateRequiredFacts checks that every fact the policy requires is present
// and correctly typed.
func (c *Compiled) validateRequiredFacts(facts decision.Facts) error {
	for _, name := range sortedFactNames(c.RequiredFacts) {
		value, ok := facts[name]
		if !ok {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"required fact %q is missing", name,
			)
		}

		if !c.RequiredFacts[name].Matches(value) {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"fact %q must be of type %s", name, c.RequiredFacts[name],
			)
		}
	}

	return nil
}

// validateOptionalFacts checks that any supplied optional fact is correctly
// typed. An absent optional fact is not an error.
func (c *Compiled) validateOptionalFacts(facts decision.Facts) error {
	for _, name := range sortedFactNames(c.OptionalFacts) {
		value, ok := facts[name]
		if !ok {
			continue
		}

		if !c.OptionalFacts[name].Matches(value) {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"fact %q must be of type %s", name, c.OptionalFacts[name],
			)
		}
	}

	return nil
}

// validateNoUndeclaredFacts rejects a fact the policy did not declare as
// either required or optional. Only called when the policy asked for strict
// input.
func (c *Compiled) validateNoUndeclaredFacts(facts decision.Facts) error {
	for _, name := range sortedFactNames(facts) {
		if _, ok := c.RequiredFacts[name]; ok {
			continue
		}

		if _, ok := c.OptionalFacts[name]; ok {
			continue
		}

		return ctxerrors.Wrapf(
			commerr.ErrValidationFailed,
			"fact %q is not declared by this policy", name,
		)
	}

	return nil
}

// sortedFactNames returns the keys of any fact-shaped map in a stable order,
// so a validation failure names the same fact on every run rather than
// whichever one Go's map iteration reached first.
func sortedFactNames[V any](source map[string]V) []string {
	names := make([]string, 0, len(source))
	for name := range source {
		names = append(names, name)
	}

	sort.Strings(names)

	return names
}
