package policy

import (
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// conditionKind tags which branch of a compiled condition node is live. The
// document allows exactly one branch per node and the compiler enforces that,
// so a compiled node never has to re-check.
type conditionKind uint8

const (
	conditionKindLeaf conditionKind = iota
	conditionKindAll
	conditionKindAny
	conditionKindNot
)

// operandKind tags which side of the fact/answer split a leaf reads.
type operandKind uint8

const (
	operandKindFact operandKind = iota
	operandKindAnswer
)

// CompiledOperand is a resolved left-hand reference. Every name in it was
// checked against the policy's declarations at compile time, so evaluation
// performs lookups without any further validation.
type CompiledOperand struct {
	kind operandKind

	// factName and factType are set when kind is operandKindFact.
	factName string
	factType decision.FactType

	// questionID, field, questionType, and probabilityKey are set when kind is
	// operandKindAnswer.
	questionID     string
	field          decision.AnswerField
	questionType   decision.QuestionType
	probabilityKey string
}

// CompiledCondition is a type-checked condition tree ready to evaluate. It is
// produced only by the compiler; the zero value is not usable.
type CompiledCondition struct {
	kind     conditionKind
	children []CompiledCondition

	operand CompiledOperand
	op      Operator

	// rightScalar holds a string, float64, or bool for every operator except
	// in and exists. rightList holds the candidate set for in. rightBool
	// holds the expected presence for exists.
	rightScalar any
	rightList   []any
	rightBool   bool
}

// Evaluate resolves the condition against the supplied facts and answers.
//
// It cannot fail. Every reference was resolved at compile time, and a
// reference that is legal but absent at runtime (an optional fact the caller
// omitted, a confidence field on an answer that carries none) makes its leaf
// false rather than raising. That keeps rule ordering, not error handling, the
// only thing that decides an outcome.
func (c CompiledCondition) Evaluate(
	facts decision.Facts,
	answers decision.Answers,
) bool {
	switch c.kind {
	case conditionKindAll:
		for _, child := range c.children {
			if !child.Evaluate(facts, answers) {
				return false
			}
		}

		return true

	case conditionKindAny:
		for _, child := range c.children {
			if child.Evaluate(facts, answers) {
				return true
			}
		}

		return false

	case conditionKindNot:
		return !c.children[0].Evaluate(facts, answers)

	case conditionKindLeaf:
		return c.evaluateLeaf(facts, answers)
	}

	return false
}

func (c CompiledCondition) evaluateLeaf(
	facts decision.Facts,
	answers decision.Answers,
) bool {
	left, present := c.resolveOperand(facts, answers)

	if c.op == OperatorExists {
		return present == c.rightBool
	}

	if !present {
		return false
	}

	switch c.op {
	case OperatorEq:
		return scalarEqual(left, c.rightScalar)
	case OperatorNeq:
		return !scalarEqual(left, c.rightScalar)
	case OperatorIn:
		for _, candidate := range c.rightList {
			if scalarEqual(left, candidate) {
				return true
			}
		}

		return false
	case OperatorLt, OperatorLte, OperatorGt, OperatorGte:
		return compareOrdered(left, c.rightScalar, c.op)
	case OperatorExists:
		return present == c.rightBool
	}

	return false
}

// resolveOperand returns the runtime value the leaf reads and whether it is
// present at all.
func (c CompiledCondition) resolveOperand(
	facts decision.Facts,
	answers decision.Answers,
) (any, bool) {
	if c.operand.kind == operandKindFact {
		value, ok := facts[c.operand.factName]

		return value, ok
	}

	answer, ok := answers[c.operand.questionID]
	if !ok {
		return nil, false
	}

	if c.operand.field == decision.AnswerFieldProbability {
		return answer.Probability(c.operand.probabilityKey)
	}

	return answer.Field(c.operand.field)
}

// scalarEqual compares two policy scalars. Numbers compare numerically across
// representations so a YAML int and a JSON float are the same value; strings
// and bools compare exactly. Mismatched kinds are never equal, which the
// compiler already rules out for declared operands but which still has to hold
// for an answer field that arrived with an unexpected shape.
func scalarEqual(left, right any) bool {
	leftNumber, leftIsNumber := decision.AsNumber(left)
	rightNumber, rightIsNumber := decision.AsNumber(right)

	if leftIsNumber && rightIsNumber {
		return leftNumber == rightNumber
	}

	if leftIsNumber != rightIsNumber {
		return false
	}

	switch typedLeft := left.(type) {
	case string:
		typedRight, ok := right.(string)

		return ok && typedLeft == typedRight
	case bool:
		typedRight, ok := right.(bool)

		return ok && typedLeft == typedRight
	}

	return false
}

// compareOrdered applies a magnitude operator. Only numbers are ordered; the
// compiler rejects an ordering operator on any other type, so a false here
// means the runtime value was not the declared type.
func compareOrdered(left, right any, op Operator) bool {
	leftNumber, ok := decision.AsNumber(left)
	if !ok {
		return false
	}

	rightNumber, ok := decision.AsNumber(right)
	if !ok {
		return false
	}

	switch op {
	case OperatorLt:
		return leftNumber < rightNumber
	case OperatorLte:
		return leftNumber <= rightNumber
	case OperatorGt:
		return leftNumber > rightNumber
	case OperatorGte:
		return leftNumber >= rightNumber
	case OperatorEq, OperatorNeq, OperatorIn, OperatorExists:
		return false
	}

	return false
}
