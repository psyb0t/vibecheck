package policy

import "errors"

// Sentinels for every way a policy document can be rejected. They are declared
// with errors.New rather than ctxerrors.New because callers match them with
// errors.Is across wrap layers.
//
// Every one of these is a compile-time or load-time rejection. None of them can
// be reached by an evaluation request; a request that violates a policy's input
// contract fails with commerr.ErrValidationFailed instead.
var (
	// ErrUnsupportedAPIVersion means apiVersion is not the one this build
	// compiles.
	ErrUnsupportedAPIVersion = errors.New("policy: unsupported apiVersion")
	// ErrUnsupportedKind means kind is not DecisionPolicy.
	ErrUnsupportedKind = errors.New("policy: unsupported kind")
	// ErrInvalidDocument means the bytes are not a well-formed policy
	// document: bad YAML, an unknown field, or a missing required section.
	ErrInvalidDocument = errors.New("policy: invalid document")
	// ErrInvalidMetadata means name, version, or description is missing or
	// outside its grammar.
	ErrInvalidMetadata = errors.New("policy: invalid metadata")
	// ErrInvalidOutcome means an outcome is malformed, duplicated, or not a
	// member of the declared outcome set.
	ErrInvalidOutcome = errors.New("policy: invalid outcome")
	// ErrInvalidQuestion means a question ID, type, instruction, or criteria
	// set is malformed.
	ErrInvalidQuestion = errors.New("policy: invalid question")
	// ErrInvalidFact means a required fact name or declared type is
	// malformed.
	ErrInvalidFact = errors.New("policy: invalid required fact")
	// ErrInvalidRule means a rule is malformed, duplicated, or unreachable.
	ErrInvalidRule = errors.New("policy: invalid rule")
	// ErrInvalidCondition means a condition tree is malformed: no branch,
	// more than one branch, an unknown operator, or an operand that does not
	// resolve.
	ErrInvalidCondition = errors.New("policy: invalid condition")
	// ErrTypeMismatch means a condition compares two operands whose types
	// cannot be compared.
	ErrTypeMismatch = errors.New("policy: condition type mismatch")
	// ErrUnknownReference means a condition names a fact, question, or
	// criteria key the policy does not declare.
	ErrUnknownReference = errors.New("policy: unknown reference")
	// ErrPolicyTooLarge means the document exceeds the configured byte
	// ceiling.
	ErrPolicyTooLarge = errors.New("policy: document too large")
	// ErrDuplicatePolicy means two loaded files declare the same name and
	// version.
	ErrDuplicatePolicy = errors.New("policy: duplicate policy identity")
	// ErrUnsafePolicyPath means a candidate file is a symlink, a device, a
	// directory, or otherwise not a plain regular file inside the policy
	// directory.
	ErrUnsafePolicyPath = errors.New("policy: unsafe policy path")
	// ErrProviderProtocol means the provider answered, but the answers do
	// not satisfy the contract the policy declared: a missing question, the
	// wrong answer type, an out-of-range number, or a distribution that does
	// not match the declared options. It is the one sentinel here that a
	// request can reach at runtime.
	ErrProviderProtocol = errors.New(
		"policy: provider response violates the question contract",
	)
)
