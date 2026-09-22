package policy

import (
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// Probability-valued answer fields are bounded to the unit interval, so a
// threshold outside it is a policy bug rather than a very strict rule.
const (
	minProbability = 0.0
	maxProbability = 1.0
)

func (c *compiler) compileCondition(
	condition Condition,
	phase rulePhase,
	ruleID string,
	depth int,
) (CompiledCondition, error) {
	if depth > MaxConditionDepth {
		return CompiledCondition{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: condition nests deeper than %d levels",
			ruleID, MaxConditionDepth,
		)
	}

	branches := 0
	if len(condition.All) > 0 {
		branches++
	}

	if len(condition.Any) > 0 {
		branches++
	}

	if condition.Not != nil {
		branches++
	}

	if condition.Left != nil {
		branches++
	}

	if branches != 1 {
		return CompiledCondition{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: needs exactly one of all, any, not, or a leaf, got %d",
			ruleID, branches,
		)
	}

	switch {
	case len(condition.All) > 0:
		return c.compileCombinator(
			condition.All, conditionKindAll, phase, ruleID, depth,
		)
	case len(condition.Any) > 0:
		return c.compileCombinator(
			condition.Any, conditionKindAny, phase, ruleID, depth,
		)
	case condition.Not != nil:
		return c.compileCombinator(
			[]Condition{*condition.Not}, conditionKindNot, phase, ruleID, depth,
		)
	}

	return c.compileLeaf(condition, phase, ruleID)
}

func (c *compiler) compileCombinator(
	children []Condition,
	kind conditionKind,
	phase rulePhase,
	ruleID string,
	depth int,
) (CompiledCondition, error) {
	if len(children) > MaxConditionsPerOp {
		return CompiledCondition{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: a combinator takes at most %d operands",
			ruleID, MaxConditionsPerOp,
		)
	}

	compiled := make([]CompiledCondition, 0, len(children))

	for _, child := range children {
		entry, err := c.compileCondition(child, phase, ruleID, depth+1)
		if err != nil {
			return CompiledCondition{}, err
		}

		compiled = append(compiled, entry)
	}

	return CompiledCondition{kind: kind, children: compiled}, nil
}

func (c *compiler) compileLeaf(
	condition Condition,
	phase rulePhase,
	ruleID string,
) (CompiledCondition, error) {
	if !condition.Op.IsValid() {
		return CompiledCondition{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: unsupported operator %q", ruleID, condition.Op,
		)
	}

	operand, err := c.resolveOperand(*condition.Left, phase, ruleID)
	if err != nil {
		return CompiledCondition{}, err
	}

	leaf := CompiledCondition{
		kind:    conditionKindLeaf,
		operand: operand,
		op:      condition.Op,
	}

	switch condition.Op {
	case OperatorExists:
		return c.compileExistsLeaf(leaf, condition.Right, operand, ruleID)
	case OperatorIn:
		return c.compileInLeaf(leaf, condition.Right, operand, ruleID)
	case OperatorEq, OperatorNeq, OperatorLt, OperatorLte, OperatorGt,
		OperatorGte:
		return c.compileScalarLeaf(
			leaf, condition.Right, operand, condition.Op, ruleID,
		)
	}

	return CompiledCondition{}, ctxerrors.Wrapf(
		ErrInvalidCondition,
		"rule %q: unsupported operator %q", ruleID, condition.Op,
	)
}

// compileExistsLeaf finishes a leaf whose operator is exists. It is the only
// operator meaningful on an absent answer field: everywhere else, absence
// means the leaf cannot even be type-checked, let alone evaluated.
func (c *compiler) compileExistsLeaf(
	leaf CompiledCondition,
	right any,
	operand CompiledOperand,
	ruleID string,
) (CompiledCondition, error) {
	expected, err := compileExistsRight(right, ruleID)
	if err != nil {
		return CompiledCondition{}, err
	}

	if operand.kind == operandKindAnswer &&
		operand.field != decision.AnswerFieldConfidence {
		return CompiledCondition{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: exists only applies to a fact or an answer confidence",
			ruleID,
		)
	}

	leaf.rightBool = expected

	return leaf, nil
}

func (c *compiler) compileInLeaf(
	leaf CompiledCondition,
	right any,
	operand CompiledOperand,
	ruleID string,
) (CompiledCondition, error) {
	list, err := c.compileRightList(right, operand, ruleID)
	if err != nil {
		return CompiledCondition{}, err
	}

	leaf.rightList = list

	return leaf, nil
}

func (c *compiler) compileScalarLeaf(
	leaf CompiledCondition,
	right any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (CompiledCondition, error) {
	scalar, err := c.compileRightScalar(right, operand, op, ruleID)
	if err != nil {
		return CompiledCondition{}, err
	}

	leaf.rightScalar = scalar

	return leaf, nil
}

func (c *compiler) resolveOperand(
	operand Operand,
	phase rulePhase,
	ruleID string,
) (CompiledOperand, error) {
	hasFact := operand.Fact != ""
	hasAnswer := operand.Answer != ""

	if hasFact == hasAnswer {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: left needs exactly one of fact or answer", ruleID,
		)
	}

	if hasFact {
		return c.resolveFactOperand(operand, ruleID)
	}

	return c.resolveAnswerOperand(operand, phase, ruleID)
}

func (c *compiler) resolveFactOperand(
	operand Operand,
	ruleID string,
) (CompiledOperand, error) {
	if operand.Field != "" {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: a fact operand must not declare a field", ruleID,
		)
	}

	factType, ok := c.facts[operand.Fact]
	if !ok {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrUnknownReference,
			"rule %q: fact %q is not declared by this policy",
			ruleID, operand.Fact,
		)
	}

	return CompiledOperand{
		kind:     operandKindFact,
		factName: operand.Fact,
		factType: factType,
	}, nil
}

func (c *compiler) resolveAnswerOperand(
	operand Operand,
	phase rulePhase,
	ruleID string,
) (CompiledOperand, error) {
	if !phase.allowsAnswers() {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: %s run before the provider and cannot reference answers",
			ruleID, phase,
		)
	}

	question, ok := c.questions[operand.Answer]
	if !ok {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrUnknownReference,
			"rule %q: question %q is not declared by this policy",
			ruleID, operand.Answer,
		)
	}

	if !operand.Field.IsValid() {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: unsupported answer field %q", ruleID, operand.Field,
		)
	}

	if !operand.Field.AvailableOn(question.Type) {
		return CompiledOperand{}, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: field %q does not exist on a %s answer",
			ruleID, operand.Field, question.Type,
		)
	}

	return CompiledOperand{
		kind:         operandKindAnswer,
		questionID:   operand.Answer,
		field:        operand.Field,
		questionType: question.Type,
	}, nil
}

// compileExistsRight normalizes the right operand of an exists check. Omitting
// it means "must be present".
func compileExistsRight(raw any, ruleID string) (bool, error) {
	if raw == nil {
		return true, nil
	}

	expected, ok := raw.(bool)
	if !ok {
		return false, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: exists takes a boolean right operand", ruleID,
		)
	}

	return expected, nil
}

func (c *compiler) compileRightList(
	raw any,
	operand CompiledOperand,
	ruleID string,
) ([]any, error) {
	entries, ok := raw.([]any)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: in takes a list right operand", ruleID,
		)
	}

	if len(entries) == 0 {
		return nil, ctxerrors.Wrapf(
			ErrInvalidCondition, "rule %q: in takes a non-empty list", ruleID,
		)
	}

	if len(entries) > MaxRightListLength {
		return nil, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: in takes at most %d candidates",
			ruleID, MaxRightListLength,
		)
	}

	values := make([]any, 0, len(entries))

	for _, entry := range entries {
		value, err := c.checkRightValue(entry, operand, OperatorIn, ruleID)
		if err != nil {
			return nil, err
		}

		values = append(values, value)
	}

	return values, nil
}

func (c *compiler) compileRightScalar(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	if raw == nil {
		return nil, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: operator %s needs a right operand", ruleID, op,
		)
	}

	return c.checkRightValue(raw, operand, op, ruleID)
}

// checkRightValue type-checks one right-hand value against the resolved left
// operand and normalizes numbers to float64 so evaluation never re-inspects
// the concrete Go type.
func (c *compiler) checkRightValue(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	if operand.kind == operandKindFact {
		return checkFactRightValue(raw, operand, op, ruleID)
	}

	return c.checkAnswerRightValue(raw, operand, op, ruleID)
}

func checkFactRightValue(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	switch operand.factType {
	case decision.FactTypeNumber:
		return checkNumberFactRightValue(raw, operand, ruleID)
	case decision.FactTypeString:
		return checkStringFactRightValue(raw, operand, op, ruleID)
	case decision.FactTypeBoolean:
		return checkBooleanFactRightValue(raw, operand, op, ruleID)
	}

	return nil, ctxerrors.Wrapf(
		ErrTypeMismatch,
		"rule %q: fact %q has no comparable type", ruleID, operand.factName,
	)
}

func checkNumberFactRightValue(
	raw any,
	operand CompiledOperand,
	ruleID string,
) (any, error) {
	number, ok := decision.AsNumber(raw)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: fact %q is a number and needs a numeric right operand",
			ruleID, operand.factName,
		)
	}

	return number, nil
}

func checkStringFactRightValue(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	if op.IsOrdering() {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: operator %s is not defined on string fact %q",
			ruleID, op, operand.factName,
		)
	}

	text, ok := raw.(string)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: fact %q is a string and needs a string right operand",
			ruleID, operand.factName,
		)
	}

	if len(text) > MaxStringValueLength {
		return nil, ctxerrors.Wrapf(
			ErrInvalidCondition,
			"rule %q: right operand exceeds %d characters",
			ruleID, MaxStringValueLength,
		)
	}

	return text, nil
}

func checkBooleanFactRightValue(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	if op != OperatorEq && op != OperatorNeq {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: operator %s is not defined on boolean fact %q",
			ruleID, op, operand.factName,
		)
	}

	value, ok := raw.(bool)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: fact %q is a boolean and needs a boolean right operand",
			ruleID, operand.factName,
		)
	}

	return value, nil
}

func (c *compiler) checkAnswerRightValue(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	if operand.field == decision.AnswerFieldChoice {
		return c.checkChoiceRightValue(raw, operand, op, ruleID)
	}

	number, ok := decision.AsNumber(raw)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: answer field %s needs a numeric right operand",
			ruleID, operand.field,
		)
	}

	if err := checkNumericAnswerRange(number, operand, ruleID); err != nil {
		return nil, err
	}

	return number, nil
}

// checkNumericAnswerRange rejects a threshold that no answer can ever reach.
// Confidence and noul are probabilities; a score is bounded by the number of
// levels the question declares.
func checkNumericAnswerRange(
	number float64,
	operand CompiledOperand,
	ruleID string,
) error {
	if operand.field == decision.AnswerFieldConfidence ||
		operand.field == decision.AnswerFieldNoul {
		if number < minProbability || number > maxProbability {
			return ctxerrors.Wrapf(
				ErrInvalidCondition,
				"rule %q: %s is a probability and must be within %g to %g",
				ruleID, operand.field, minProbability, maxProbability,
			)
		}
	}

	return nil
}

func (c *compiler) checkChoiceRightValue(
	raw any,
	operand CompiledOperand,
	op Operator,
	ruleID string,
) (any, error) {
	if op.IsOrdering() {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: operator %s is not defined on a choice answer",
			ruleID, op,
		)
	}

	key, ok := raw.(string)
	if !ok {
		return nil, ctxerrors.Wrapf(
			ErrTypeMismatch,
			"rule %q: choice comparison needs a declared option key", ruleID,
		)
	}

	question := c.questions[operand.questionID]
	if _, declared := question.ChoiceCriteria[key]; !declared {
		return nil, ctxerrors.Wrapf(
			ErrUnknownReference,
			"rule %q: question %q does not declare option %q",
			ruleID, operand.questionID, key,
		)
	}

	return key, nil
}
