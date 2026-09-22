package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/core/evaluations"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
)

// failure is one mapped error: the HTTP status the caller sees and the stable
// envelope that goes with it.
type failure struct {
	status int
	body   api.Error
}

// classify maps a core error onto its HTTP status and stable error code.
//
// The order matters. The more specific service sentinels are checked before
// the generic commerr ones, because a core error usually wraps both.
//
// Nothing in here ever puts the provider's own message into the response: a
// provider error body can echo the caller's state back, and that must not
// leave the process.
func classify(err error) failure {
	switch {
	case err == nil:
		return failure{status: http.StatusOK}

	case errors.Is(err, evaluations.ErrIdempotencyConflict):
		return newFailure(
			http.StatusConflict, aichteeteapee.ErrorCodeConflict,
			"the idempotency key was reused with a different request",
		)

	case errors.Is(err, evaluations.ErrPolicySelection):
		return newFailure(
			http.StatusBadRequest, aichteeteapee.ErrorCodeBadRequest,
			"supply exactly one of policyRef or inlinePolicy",
		)

	case errors.Is(err, evaluations.ErrInlinePoliciesDisabled):
		return newFailure(
			http.StatusBadRequest, aichteeteapee.ErrorCodeBadRequest,
			"inline policies are disabled on this deployment",
		)

	case errors.Is(err, commerr.ErrNotFound):
		return newFailure(
			http.StatusNotFound, aichteeteapee.ErrorCodeNotFound,
			"no such resource",
		)

	case errors.Is(err, commerr.ErrValidationFailed),
		errors.Is(err, commerr.ErrInvalidArgument):
		return newFailure(
			http.StatusUnprocessableEntity,
			aichteeteapee.ErrorCodeValidationFailed,
			"the request failed validation",
		)

	case errors.Is(err, policy.ErrPolicyTooLarge):
		return newFailure(
			http.StatusRequestEntityTooLarge,
			aichteeteapee.ErrorCodeFromHTTPStatus(
				http.StatusRequestEntityTooLarge,
			),
			"the supplied policy document is too large",
		)
	}

	return classifyProvider(err)
}

// classifyProvider maps the provider-shaped failures, which are the ones a
// caller can usefully retry or escalate on.
func classifyProvider(err error) failure {
	switch {
	case errors.Is(err, context.Canceled):
		return newFailure(
			statusClientClosedRequest, aichteeteapee.ErrorCodeBadRequest,
			"the client closed the request",
		)

	case errors.Is(err, commerr.ErrTimeout),
		errors.Is(err, context.DeadlineExceeded):
		return newFailure(
			http.StatusGatewayTimeout, aichteeteapee.ErrorCodeGatewayTimeout,
			"the decision provider did not answer in time",
		)

	case errors.Is(err, commerr.ErrRateLimited):
		return newFailure(
			http.StatusTooManyRequests, aichteeteapee.ErrorCodeTooManyRequests,
			"the decision provider rate limit was reached",
		)

	case errors.Is(err, commerr.ErrNotAuthenticated),
		errors.Is(err, commerr.ErrRequiredConfigValueNotSet):
		return newFailure(
			http.StatusServiceUnavailable,
			aichteeteapee.ErrorCodeServiceUnavailable,
			"the decision provider is not configured",
		)

	case errors.Is(err, commerr.ErrUnavailable),
		errors.Is(err, commerr.ErrConnectFailed),
		errors.Is(err, provider.ErrOverloaded):
		return newFailure(
			http.StatusServiceUnavailable,
			aichteeteapee.ErrorCodeServiceUnavailable,
			"the decision provider is unavailable",
		)

	case errors.Is(err, commerr.ErrParseFailed),
		errors.Is(err, provider.ErrResponseTooLarge),
		errors.Is(err, policy.ErrProviderProtocol):
		return newFailure(
			http.StatusBadGateway, aichteeteapee.ErrorCodeBadGateway,
			"the decision provider returned an unusable response",
		)
	}

	return newFailure(
		http.StatusInternalServerError,
		aichteeteapee.ErrorCodeInternalServerError,
		"the request could not be completed",
	)
}

// statusClientClosedRequest is nginx's 499. Go has no constant for it, and a
// cancelled request is not a server error, so recording it separately keeps
// the 5xx rate honest.
const statusClientClosedRequest = 499

func newFailure(status int, code, message string) failure {
	return failure{
		status: status,
		body:   api.Error{Code: code, Message: message},
	}
}

// badRequest is the shape-validation failure a handler raises itself, before
// any service call.
func badRequest(message string) api.Error {
	return api.Error{
		Code:    aichteeteapee.ErrorCodeBadRequest,
		Message: message,
	}
}

// classifyRecordedFailure maps an evaluation that was persisted but produced
// no outcome onto the status the contract declares for it.
//
// The core treats a provider failure as a recorded outcome and returns no
// error, because the audit row has to exist either way. The transport still
// owes the caller a failure status. An evaluation with an empty outcome
// returned as 201 reads as success to anything that does not inspect the
// status field, and a decision that was never made must not look like one
// that allowed something.
//
// The evaluation ID travels in details, so the caller can still fetch the row.
func classifyRecordedFailure(result api.Evaluation) (failure, bool) {
	if result.Status == api.Completed {
		return failure{}, false
	}

	code := ""
	if result.FailureCode != nil {
		code = *result.FailureCode
	}

	mapped := recordedFailureStatus(code)
	mapped.body.Details = map[string]any{
		"evaluationId": result.Id.String(),
		"failureCode":  code,
	}

	return mapped, true
}

// recordedFailureStatus turns a stable failure code into its declared status.
func recordedFailureStatus(code string) failure {
	switch code {
	case evaluations.FailureCodeProviderTimeout.String():
		return newFailure(
			http.StatusGatewayTimeout, aichteeteapee.ErrorCodeGatewayTimeout,
			"the provider did not answer in time",
		)

	case evaluations.FailureCodeProviderRateLimited.String(),
		evaluations.FailureCodeProviderUnavailable.String():
		return newFailure(
			http.StatusServiceUnavailable,
			aichteeteapee.ErrorCodeServiceUnavailable,
			"the provider is unavailable",
		)

	case evaluations.FailureCodeInputInvalid.String():
		return newFailure(
			http.StatusUnprocessableEntity,
			aichteeteapee.ErrorCodeUnprocessableEntity,
			"the request did not satisfy the policy contract",
		)
	}

	// Auth, protocol, and rejected-request failures are the upstream
	// answering wrongly rather than being down.
	return newFailure(
		http.StatusBadGateway, aichteeteapee.ErrorCodeBadGateway,
		"the provider did not return a usable answer",
	)
}
