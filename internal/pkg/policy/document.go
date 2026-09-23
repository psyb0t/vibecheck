package policy

import "github.com/psyb0t/vibecheck/internal/pkg/common/decision"

// The document identity this build compiles. A file declaring anything else is
// rejected rather than guessed at, so a future schema revision can never be
// silently reinterpreted by an old binary.
const (
	APIVersion = "vibecheck.psyb0t.dev/v1alpha1"
	Kind       = "DecisionPolicy"
)

// Structural ceilings on a policy document. They bound how much work one
// compiled policy can ask the evaluator and the provider to do, and they are
// what make an inline policy safe to accept from a request body.
const (
	MaxOutcomes        = 32
	MaxQuestions       = 64
	MaxRequiredFacts   = 128
	MaxRules           = 128
	MaxConditionDepth  = 8
	MaxConditionsPerOp = 32
	MaxRightListLength = 64

	// MinChoiceCriteria and MaxChoiceCriteria mirror the TypeSafe Choice
	// contract: at least two options, at most 255.
	MinChoiceCriteria = 2
	MaxChoiceCriteria = 255

	// MinScoreCriteria and MaxScoreCriteria mirror the TypeSafe Score
	// contract: at least two ordered levels, at most 10.
	MinScoreCriteria = 2
	MaxScoreCriteria = 10

	MaxDescriptionLength  = 512
	MaxInstructionsLength = 4096
	MaxCriteriaTextLength = 1024
	MaxQuestionValueDepth = 16
	MaxQuestionValueNodes = 512
	MaxVersionLength      = 32
	MaxStringValueLength  = 1024
)

// Operator is a scalar comparison a condition leaf can apply.
type Operator string

const (
	OperatorEq     Operator = "eq"
	OperatorNeq    Operator = "neq"
	OperatorLt     Operator = "lt"
	OperatorLte    Operator = "lte"
	OperatorGt     Operator = "gt"
	OperatorGte    Operator = "gte"
	OperatorIn     Operator = "in"
	OperatorExists Operator = "exists"
)

func (o Operator) String() string {
	return string(o)
}

func (o Operator) IsValid() bool {
	switch o {
	case OperatorEq, OperatorNeq, OperatorLt, OperatorLte,
		OperatorGt, OperatorGte, OperatorIn, OperatorExists:
		return true
	}

	return false
}

// IsOrdering reports whether the operator compares magnitude, which only
// numbers support.
func (o Operator) IsOrdering() bool {
	switch o {
	case OperatorLt, OperatorLte, OperatorGt, OperatorGte:
		return true
	case OperatorEq, OperatorNeq, OperatorIn, OperatorExists:
		return false
	}

	return false
}

// Document is the on-disk and on-the-wire shape of a policy. It is a pure
// data carrier: decoding one performs no validation beyond what the decoder
// itself enforces, and every semantic rule lives in Compile.
//
// Every field carries both a yaml and a json tag so the same struct decodes a
// mounted YAML file and an inline policy in a JSON request body.
type Document struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind"       yaml:"kind"`
	Metadata   Metadata `json:"metadata"   yaml:"metadata"`
	Spec       Spec     `json:"spec"       yaml:"spec"`
}

// Metadata is the policy's identity. The pair (Name, Version) is the identity;
// Description is documentation only and never affects behavior.
type Metadata struct {
	Name        string `json:"name"                  yaml:"name"`
	Version     string `json:"version"               yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"` //nolint:lll // dual json/yaml struct tag cannot be split
}

// Spec is the decision behavior.
type Spec struct {
	Model          string              `json:"model,omitempty"         yaml:"model,omitempty"`         //nolint:lll // dual json/yaml struct tag cannot be split
	Outcomes       []decision.Outcome  `json:"outcomes"                yaml:"outcomes"`                //nolint:lll // dual json/yaml struct tag cannot be split
	DefaultOutcome decision.Outcome    `json:"defaultOutcome"          yaml:"defaultOutcome"`          //nolint:lll // dual json/yaml struct tag cannot be split
	Input          Input               `json:"input"                   yaml:"input,omitempty"`         //nolint:lll // dual json/yaml struct tag cannot be split
	PreRules       []Rule              `json:"preRules,omitempty"      yaml:"preRules,omitempty"`      //nolint:lll // dual json/yaml struct tag cannot be split
	Questions      map[string]Question `json:"questions"               yaml:"questions"`               //nolint:lll // dual json/yaml struct tag cannot be split
	DecisionRules  []Rule              `json:"decisionRules,omitempty" yaml:"decisionRules,omitempty"` //nolint:lll // dual json/yaml struct tag cannot be split
}

// Input declares the trusted facts a caller may supply.
type Input struct {
	// RequiredFacts must all be present and correctly typed or the request
	// is rejected before any rule runs.
	RequiredFacts map[string]decision.FactType `json:"requiredFacts,omitempty" yaml:"requiredFacts,omitempty"` //nolint:lll // dual json/yaml struct tag cannot be split

	// OptionalFacts may be absent. They are type-checked when present, and
	// they are what makes the exists operator meaningful: a condition over a
	// required fact is trivially present by the time any rule runs.
	OptionalFacts map[string]decision.FactType `json:"optionalFacts,omitempty" yaml:"optionalFacts,omitempty"` //nolint:lll // dual json/yaml struct tag cannot be split

	// Strict rejects a request carrying any fact the policy does not
	// declare. Off by default so a caller can attach extra context without
	// breaking on every policy revision.
	Strict bool `json:"strict,omitempty" yaml:"strict,omitempty"`
}

// Question is one typed Jev question.
//
// Criteria is deliberately untyped in the document because its shape depends
// on Type: an ordered list of level descriptions for a score, a key to
// description map for a choice, and absent for a noul. Compile resolves it
// into the typed CompiledQuestion.
type Question struct {
	Type         decision.QuestionType `json:"type"                   yaml:"type"`                   //nolint:lll // aligned dual tags cannot be split
	Instructions any                   `json:"instructions,omitempty" yaml:"instructions,omitempty"` //nolint:lll // dual json/yaml struct tag cannot be split
	Criteria     any                   `json:"criteria,omitempty"     yaml:"criteria,omitempty"`     //nolint:lll // dual json/yaml struct tag cannot be split
}

// Rule is one entry in preRules or decisionRules.
//
// When is optional: a rule with no condition is unconditional and always
// matches. The compiler allows at most one such rule and only as the last
// entry in its phase, because anything after it is dead.
type Rule struct {
	ID          string           `json:"id"                    yaml:"id"`
	Description string           `json:"description,omitempty" yaml:"description,omitempty"` //nolint:lll // dual json/yaml struct tag cannot be split
	When        *Condition       `json:"when,omitempty"        yaml:"when,omitempty"`        //nolint:lll // dual json/yaml struct tag cannot be split
	Outcome     decision.Outcome `json:"outcome"               yaml:"outcome"`
}

// Condition is one node of the condition tree. Exactly one of All, Any, Not, or
// the leaf trio (Left, Op, Right) must be set.
type Condition struct {
	All []Condition `json:"all,omitempty" yaml:"all,omitempty"`
	Any []Condition `json:"any,omitempty" yaml:"any,omitempty"`
	Not *Condition  `json:"not,omitempty" yaml:"not,omitempty"`

	Left  *Operand `json:"left,omitempty"  yaml:"left,omitempty"`
	Op    Operator `json:"op,omitempty"    yaml:"op,omitempty"`
	Right any      `json:"right,omitempty" yaml:"right,omitempty"`
}

// Operand is the left side of a condition leaf. Exactly one of Fact or Answer
// must be set. Field is required when Answer is set and forbidden otherwise.
// ProbabilityKey selects one choice option or score level when Field is
// probability.
type Operand struct {
	Fact           string               `json:"fact,omitempty"           yaml:"fact,omitempty"`           //nolint:lll // dual JSON/YAML struct tag cannot be split
	Answer         string               `json:"answer,omitempty"         yaml:"answer,omitempty"`         //nolint:lll // dual JSON/YAML struct tag cannot be split
	Field          decision.AnswerField `json:"field,omitempty"          yaml:"field,omitempty"`          //nolint:lll // dual JSON/YAML struct tag cannot be split
	ProbabilityKey string               `json:"probabilityKey,omitempty" yaml:"probabilityKey,omitempty"` //nolint:lll // dual JSON/YAML struct tag cannot be split
}
