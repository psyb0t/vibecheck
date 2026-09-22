package evaluations

import "errors"

// Failure classes the transports map to their own status codes. Everything
// that already has a commerr equivalent uses that instead; only the cases
// commerr does not cover are declared here.
var (
	// ErrInlinePoliciesDisabled means the request carried an inline policy
	// and the deployment did not enable them.
	ErrInlinePoliciesDisabled = errors.New(
		"evaluations: inline policies are disabled on this deployment",
	)

	// ErrPolicySelection means the request did not select exactly one
	// policy: it supplied both a reference and an inline document, or
	// neither.
	ErrPolicySelection = errors.New(
		"evaluations: exactly one of policyRef or inlinePolicy is required",
	)

	// ErrIdempotencyConflict means the idempotency key was reused with a
	// different canonical request.
	ErrIdempotencyConflict = errors.New(
		"evaluations: idempotency key was reused with a different request",
	)
)

// FailureCode is the stable machine-readable class recorded on an evaluation
// that did not complete, and reported to the caller. It never contains
// provider text.
type FailureCode string

const (
	FailureCodeProviderAuth        FailureCode = "PROVIDER_AUTH_FAILED"
	FailureCodeProviderRateLimited FailureCode = "PROVIDER_RATE_LIMITED"
	FailureCodeProviderUnavailable FailureCode = "PROVIDER_UNAVAILABLE"
	FailureCodeProviderTimeout     FailureCode = "PROVIDER_TIMEOUT"
	FailureCodeProviderProtocol    FailureCode = "PROVIDER_PROTOCOL_ERROR"
	FailureCodeProviderRejected    FailureCode = "PROVIDER_REJECTED_REQUEST"
	FailureCodeInputInvalid        FailureCode = "INPUT_VALIDATION_FAILED"
)

func (c FailureCode) String() string {
	return string(c)
}
