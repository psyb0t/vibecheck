package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// The canonical form is a projection of the compiled policy, not of the source
// bytes. Reformatting a document, reordering its questions, or rewording a
// comment leaves the hash alone; changing any declared behavior changes it.
//
// encoding/json sorts map keys, and every slice below is already in a
// deterministic order, so marshaling is reproducible without a bespoke
// serializer.
type canonicalPolicy struct {
	APIVersion     string                       `json:"apiVersion"`
	Kind           string                       `json:"kind"`
	Name           string                       `json:"name"`
	Version        string                       `json:"version"`
	Description    string                       `json:"description"`
	Model          string                       `json:"model"`
	Outcomes       []decision.Outcome           `json:"outcomes"`
	DefaultOutcome decision.Outcome             `json:"defaultOutcome"`
	RequiredFacts  map[string]decision.FactType `json:"requiredFacts"`
	OptionalFacts  map[string]decision.FactType `json:"optionalFacts"`
	StrictInput    bool                         `json:"strictInput"`
	Questions      []canonicalQuestion          `json:"questions"`
	PreRules       []canonicalRule              `json:"preRules"`
	DecisionRules  []canonicalRule              `json:"decisionRules"`
}

type canonicalQuestion struct {
	ID             string                `json:"id"`
	Type           decision.QuestionType `json:"type"`
	Instructions   any                   `json:"instructions,omitempty"`
	NoulCriteria   map[string]any        `json:"noulCriteria,omitempty"`
	ChoiceCriteria map[string]any        `json:"choiceCriteria,omitempty"`
	ScoreCriteria  []any                 `json:"scoreCriteria,omitempty"`
}

type canonicalRule struct {
	ID          string              `json:"id"`
	Description string              `json:"description"`
	Outcome     decision.Outcome    `json:"outcome"`
	When        *canonicalCondition `json:"when,omitempty"`
}

type canonicalCondition struct {
	Kind     string               `json:"kind"`
	Children []canonicalCondition `json:"children,omitempty"`

	Fact           string `json:"fact,omitempty"`
	Question       string `json:"question,omitempty"`
	Field          string `json:"field,omitempty"`
	ProbabilityKey string `json:"probabilityKey,omitempty"`

	Op          string `json:"op,omitempty"`
	RightScalar any    `json:"rightScalar,omitempty"`
	RightList   []any  `json:"rightList,omitempty"`
	RightBool   bool   `json:"rightBool,omitempty"`
}

// canonicalize renders the compiled policy to its canonical JSON and returns
// that JSON together with its lowercase hex SHA-256.
func canonicalize(compiled *Compiled) ([]byte, string, error) {
	questions := make([]canonicalQuestion, 0, len(compiled.QuestionIDs))

	for _, id := range compiled.QuestionIDs {
		question := compiled.Questions[id]
		questions = append(questions, canonicalQuestion{
			ID:             question.ID,
			Type:           question.Type,
			Instructions:   question.Instructions,
			NoulCriteria:   question.NoulCriteria,
			ChoiceCriteria: question.ChoiceCriteria,
			ScoreCriteria:  question.ScoreCriteria,
		})
	}

	form := canonicalPolicy{
		APIVersion:     APIVersion,
		Kind:           Kind,
		Name:           compiled.Ref.Name,
		Version:        compiled.Ref.Version,
		Description:    compiled.Description,
		Model:          compiled.Model,
		Outcomes:       compiled.Outcomes,
		DefaultOutcome: compiled.DefaultOutcome,
		RequiredFacts:  compiled.RequiredFacts,
		OptionalFacts:  compiled.OptionalFacts,
		StrictInput:    compiled.StrictInput,
		Questions:      questions,
		PreRules:       canonicalRules(compiled.PreRules),
		DecisionRules:  canonicalRules(compiled.DecisionRules),
	}

	encoded, err := json.Marshal(form)
	if err != nil {
		return nil, "", ctxerrors.Wrap(err, "marshal canonical policy")
	}

	sum := sha256.Sum256(encoded)

	return encoded, hex.EncodeToString(sum[:]), nil
}

func canonicalRules(rules []CompiledRule) []canonicalRule {
	out := make([]canonicalRule, 0, len(rules))

	for _, rule := range rules {
		entry := canonicalRule{
			ID:          rule.ID,
			Description: rule.Description,
			Outcome:     rule.Outcome,
		}

		if rule.Condition != nil {
			condition := canonicalizeCondition(*rule.Condition)
			entry.When = &condition
		}

		out = append(out, entry)
	}

	return out
}

func canonicalizeCondition(condition CompiledCondition) canonicalCondition {
	form := canonicalCondition{Kind: conditionKindName(condition.kind)}

	if len(condition.children) > 0 {
		children := make([]canonicalCondition, 0, len(condition.children))
		for _, child := range condition.children {
			children = append(children, canonicalizeCondition(child))
		}

		form.Children = children

		return form
	}

	form.Op = condition.op.String()
	form.RightScalar = condition.rightScalar
	form.RightList = condition.rightList
	form.RightBool = condition.rightBool

	if condition.operand.kind == operandKindFact {
		form.Fact = condition.operand.factName

		return form
	}

	form.Question = condition.operand.questionID
	form.Field = condition.operand.field.String()
	form.ProbabilityKey = condition.operand.probabilityKey

	return form
}

func conditionKindName(kind conditionKind) string {
	switch kind {
	case conditionKindAll:
		return "all"
	case conditionKindAny:
		return "any"
	case conditionKindNot:
		return "not"
	case conditionKindLeaf:
		return "leaf"
	}

	return "unknown"
}
