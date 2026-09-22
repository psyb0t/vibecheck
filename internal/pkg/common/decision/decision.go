// Package decision holds the vocabulary every Vibecheck layer shares: the
// outcomes a policy can select, the typed answers a provider produces, and the
// trusted facts a caller supplies.
//
// It imports nothing else in this project on purpose. Policy compilation, the
// provider adapter, the application core, and both transports all speak these
// types without any of them importing another.
package decision

import "regexp"

// Identifier length ceilings. They exist so a policy document cannot smuggle an
// unbounded string into a log line, a metric label, or a database column.
const (
	MaxIdentifierLength = 64
	MaxFactNameLength   = 128
)

// The bounded identifier grammars. Every name a policy author chooses is
// matched against exactly one of these before the compiler accepts it.
var (
	identifierPattern = regexp.MustCompile(
		`^[A-Za-z][A-Za-z0-9_]*$`,
	)
	dashedIdentifierPattern = regexp.MustCompile(
		`^[A-Za-z][A-Za-z0-9_-]*$`,
	)
	factNamePattern = regexp.MustCompile(
		`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)*$`,
	)
)

// IsIdentifier reports whether name is a valid question ID or criteria key.
func IsIdentifier(name string) bool {
	return len(name) <= MaxIdentifierLength &&
		identifierPattern.MatchString(name)
}

// IsDashedIdentifier reports whether name is a valid outcome or rule ID.
// Hyphens are allowed because rule IDs read as sentences.
func IsDashedIdentifier(name string) bool {
	return len(name) <= MaxIdentifierLength &&
		dashedIdentifierPattern.MatchString(name)
}

// IsFactName reports whether name is a valid dotted fact path such as
// target.ownedBySession. A dot separates segments; it is not a legal character
// inside one.
func IsFactName(name string) bool {
	return len(name) <= MaxFactNameLength && factNamePattern.MatchString(name)
}

// Outcome is a decision a policy can return. The set is declared per policy, so
// Vibecheck itself never hardcodes allow, review, or block.
type Outcome string

func (o Outcome) String() string {
	return string(o)
}

// IsValid reports whether the outcome matches the bounded identifier grammar.
// It says nothing about whether a particular policy declares this outcome.
func (o Outcome) IsValid() bool {
	return IsDashedIdentifier(string(o))
}

// QuestionType mirrors the three TypeSafe Jev primitives. Vibecheck
// deliberately does not invent a fourth.
type QuestionType string

const (
	QuestionTypeChoice QuestionType = "choice"
	QuestionTypeScore  QuestionType = "score"
	QuestionTypeNoul   QuestionType = "noul"
)

func (q QuestionType) String() string {
	return string(q)
}

func (q QuestionType) IsValid() bool {
	switch q {
	case QuestionTypeChoice, QuestionTypeScore, QuestionTypeNoul:
		return true
	}

	return false
}

// AnswerField names the part of an answer that a condition compares against.
type AnswerField string

const (
	AnswerFieldChoice     AnswerField = "choice"
	AnswerFieldScore      AnswerField = "score"
	AnswerFieldNoul       AnswerField = "noul"
	AnswerFieldConfidence AnswerField = "confidence"
)

func (f AnswerField) String() string {
	return string(f)
}

func (f AnswerField) IsValid() bool {
	switch f {
	case AnswerFieldChoice, AnswerFieldScore, AnswerFieldNoul,
		AnswerFieldConfidence:
		return true
	}

	return false
}

// AvailableOn reports whether this field exists on an answer of the given
// question type. A Noul answer carries no confidence, which is why the matrix
// is spelled out rather than inferred from the field name.
func (f AnswerField) AvailableOn(questionType QuestionType) bool {
	switch questionType {
	case QuestionTypeChoice:
		return f == AnswerFieldChoice || f == AnswerFieldConfidence
	case QuestionTypeScore:
		return f == AnswerFieldScore || f == AnswerFieldConfidence
	case QuestionTypeNoul:
		return f == AnswerFieldNoul
	}

	return false
}

// IsNumeric reports whether this field holds a number. Only the choice field
// is a string.
func (f AnswerField) IsNumeric() bool {
	return f != AnswerFieldChoice
}

// FactType is the declared type of a required fact. Facts are trusted caller
// input, so the declared type is what the evaluator enforces before any rule
// compares against the value.
type FactType string

const (
	FactTypeString  FactType = "string"
	FactTypeBoolean FactType = "boolean"
	FactTypeNumber  FactType = "number"
)

func (t FactType) String() string {
	return string(t)
}

func (t FactType) IsValid() bool {
	switch t {
	case FactTypeString, FactTypeBoolean, FactTypeNumber:
		return true
	}

	return false
}

// Matches reports whether value is an instance of this declared fact type.
// JSON decoding yields float64 for every number, so the integer kinds are
// accepted for FactTypeNumber alongside the float kinds.
func (t FactType) Matches(value any) bool {
	switch t {
	case FactTypeString:
		_, ok := value.(string)

		return ok
	case FactTypeBoolean:
		_, ok := value.(bool)

		return ok
	case FactTypeNumber:
		_, ok := AsNumber(value)

		return ok
	}

	return false
}

// AsNumber converts any supported numeric representation to float64. It exists
// because a fact can arrive as a JSON float, a YAML int, or a Go int depending
// on which decoder produced it.
func AsNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	}

	return 0, false
}

// Facts are the trusted structured values a caller supplies alongside state.
// Values are JSON scalars: string, bool, or number.
type Facts map[string]any

// Answer is one typed Jev result.
//
// It is a single struct rather than an interface because every answer is
// persisted verbatim in the evaluation audit record, and a flat shape survives
// that round trip without a custom unmarshaler.
//
// Only the fields belonging to Type are populated. HasConfidence distinguishes
// a confidence of zero from a question type that has no confidence at all.
type Answer struct {
	Type QuestionType `json:"type"`

	// Choice is the selected criteria key. QuestionTypeChoice only.
	Choice string `json:"choice,omitempty"`

	// Score is the position on the level spectrum and may land between two
	// levels. QuestionTypeScore only.
	Score float64 `json:"score,omitempty"`

	// Noul is the probability that the proposition is true, 0 through 1.
	// QuestionTypeNoul only.
	Noul float64 `json:"noul,omitempty"`

	// Confidence is how certain the model is, 0 through 1. Choice and score
	// only. It is one input to policy rules, never a claim of correctness.
	Confidence    float64 `json:"confidence,omitempty"`
	HasConfidence bool    `json:"hasConfidence"`

	// Probabilities is keyed by criteria key for a choice, and by the level
	// index rendered as a decimal string for a score.
	Probabilities map[string]float64 `json:"probabilities,omitempty"`

	// Legend maps a score level index to its description. Score only.
	Legend map[string]string `json:"legend,omitempty"`
}

// Field returns the value of one answer field and whether that field exists on
// this answer.
func (a Answer) Field(field AnswerField) (any, bool) {
	if !field.AvailableOn(a.Type) {
		return nil, false
	}

	switch field {
	case AnswerFieldChoice:
		return a.Choice, true
	case AnswerFieldScore:
		return a.Score, true
	case AnswerFieldNoul:
		return a.Noul, true
	case AnswerFieldConfidence:
		if !a.HasConfidence {
			return nil, false
		}

		return a.Confidence, true
	}

	return nil, false
}

// Answers maps question ID to the answer the provider returned for it.
type Answers map[string]Answer

// ExecutionPath records how far an evaluation got before an outcome was
// selected. It is used as a bounded metric label, so the set stays small.
type ExecutionPath string

const (
	// ExecutionPathPreRule means a deterministic pre-rule matched and no
	// provider call was made.
	ExecutionPathPreRule ExecutionPath = "pre_rule"
	// ExecutionPathDecisionRule means a decision rule matched after the
	// provider answered.
	ExecutionPathDecisionRule ExecutionPath = "decision_rule"
	// ExecutionPathDefault means nothing matched and the default outcome
	// applied.
	ExecutionPathDefault ExecutionPath = "default"
)

func (p ExecutionPath) String() string {
	return string(p)
}

func (p ExecutionPath) IsValid() bool {
	switch p {
	case ExecutionPathPreRule, ExecutionPathDecisionRule, ExecutionPathDefault:
		return true
	}

	return false
}
