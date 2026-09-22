package policies

import (
	"encoding/json"
	"maps"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// CompiledToAPI projects a compiled policy onto its public shape.
//
// It deliberately exposes rule IDs rather than rule bodies. A rule ID is what
// an evaluation reports and what an operator correlates against; the
// condition tree is an implementation detail of the mounted file, and the
// canonical document is already addressable by hash.
func CompiledToAPI(compiled *policy.Compiled) api.Policy {
	outcomes := make([]string, 0, len(compiled.Outcomes))
	for _, outcome := range compiled.Outcomes {
		outcomes = append(outcomes, outcome.String())
	}

	questions := make([]api.PolicyQuestion, 0, len(compiled.QuestionIDs))
	for _, id := range compiled.QuestionIDs {
		questions = append(questions, questionToAPI(compiled.Questions[id]))
	}

	return api.Policy{
		Name:            compiled.Ref.Name,
		Version:         compiled.Ref.Version,
		Description:     optionalString(compiled.Description),
		Hash:            compiled.Hash,
		Model:           optionalString(compiled.Model),
		Outcomes:        outcomes,
		DefaultOutcome:  compiled.DefaultOutcome.String(),
		RequiredFacts:   factTypesToAPI(compiled.RequiredFacts),
		OptionalFacts:   factTypesToAPI(compiled.OptionalFacts),
		StrictInput:     &compiled.StrictInput,
		Questions:       questions,
		PreRuleIds:      ruleIDsToAPI(compiled.PreRules),
		DecisionRuleIds: ruleIDsToAPI(compiled.DecisionRules),
	}
}

func questionToAPI(question policy.CompiledQuestion) api.PolicyQuestion {
	entry := api.PolicyQuestion{
		Id:           question.ID,
		Type:         api.QuestionType(question.Type),
		Instructions: question.Instructions,
	}

	if len(question.ChoiceCriteria) > 0 {
		criteria := make(map[string]string, len(question.ChoiceCriteria))
		maps.Copy(criteria, question.ChoiceCriteria)

		entry.ChoiceCriteria = &criteria
	}

	if len(question.ScoreCriteria) > 0 {
		levels := append([]string(nil), question.ScoreCriteria...)
		entry.ScoreCriteria = &levels
	}

	return entry
}

func factTypesToAPI(
	source map[string]decision.FactType,
) *map[string]string {
	if len(source) == 0 {
		return nil
	}

	projected := make(map[string]string, len(source))
	for name, factType := range source {
		projected[name] = string(factType)
	}

	return &projected
}

func ruleIDsToAPI(rules []policy.CompiledRule) *[]string {
	if len(rules) == 0 {
		return nil
	}

	ids := make([]string, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}

	return &ids
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

// encodeDocument re-encodes a decoded JSON object so the one policy parser
// consumes it. Inline and mounted policies must go through the same compiler
// or they drift, and this is the join point.
func encodeDocument(document map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ctxerrors.Wrap(
			commerr.ErrMarshalFailed, "re-encode the supplied policy document",
		)
	}

	return encoded, nil
}
