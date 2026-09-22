package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/core/evaluations"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	"github.com/stretchr/testify/assert"
)

// wrapForTest mimics how the core actually produces a wrapped error: a
// sentinel travelling up through one or more ctxerrors.Wrap calls.
func wrapForTest(sentinel error) error {
	return ctxerrors.Wrap(sentinel, "wrapped for test")
}

func TestClassifyMapsEveryBranchToItsStatus(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"nil", nil, http.StatusOK},
		{
			"idempotency conflict",
			wrapForTest(evaluations.ErrIdempotencyConflict),
			http.StatusConflict,
		},
		{
			"policy selection",
			wrapForTest(evaluations.ErrPolicySelection),
			http.StatusBadRequest,
		},
		{
			"inline policies disabled",
			wrapForTest(evaluations.ErrInlinePoliciesDisabled),
			http.StatusBadRequest,
		},
		{
			"not found",
			wrapForTest(commerr.ErrNotFound),
			http.StatusNotFound,
		},
		{
			"validation failed",
			wrapForTest(commerr.ErrValidationFailed),
			http.StatusUnprocessableEntity,
		},
		{
			"invalid argument",
			wrapForTest(commerr.ErrInvalidArgument),
			http.StatusUnprocessableEntity,
		},
		{
			"policy too large",
			wrapForTest(policy.ErrPolicyTooLarge),
			http.StatusRequestEntityTooLarge,
		},
		{
			"context canceled",
			wrapForTest(context.Canceled),
			statusClientClosedRequest,
		},
		{
			"commerr timeout",
			wrapForTest(commerr.ErrTimeout),
			http.StatusGatewayTimeout,
		},
		{
			"context deadline exceeded",
			wrapForTest(context.DeadlineExceeded),
			http.StatusGatewayTimeout,
		},
		{
			"rate limited",
			wrapForTest(commerr.ErrRateLimited),
			http.StatusTooManyRequests,
		},
		{
			"not authenticated",
			wrapForTest(commerr.ErrNotAuthenticated),
			http.StatusServiceUnavailable,
		},
		{
			"unavailable",
			wrapForTest(commerr.ErrUnavailable),
			http.StatusServiceUnavailable,
		},
		{
			"provider overloaded",
			wrapForTest(provider.ErrOverloaded),
			http.StatusServiceUnavailable,
		},
		{
			"parse failed",
			wrapForTest(commerr.ErrParseFailed),
			http.StatusBadGateway,
		},
		{
			"provider response too large",
			wrapForTest(provider.ErrResponseTooLarge),
			http.StatusBadGateway,
		},
		{
			"provider protocol",
			wrapForTest(policy.ErrProviderProtocol),
			http.StatusBadGateway,
		},
		{
			"unknown error",
			wrapForTest(ctxerrors.New("something unmapped")),
			http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := classify(tc.err)
			assert.Equal(t, tc.wantStatus, got.status)

			if tc.err == nil {
				assert.Empty(t, got.body.Code)

				return
			}

			assert.NotEmpty(
				t, got.body.Code,
				"every mapped failure carries a stable machine-readable code",
			)
		})
	}
}

// TestClassifyNeverLeaksUpstreamErrorText is the provider-response-leak
// guard: a provider error's own text can echo the caller's state back, and
// that text must never reach the response envelope, only the fixed,
// hand-written message for that failure class.
func TestClassifyNeverLeaksUpstreamErrorText(t *testing.T) {
	t.Parallel()

	const marker = "UPSTREAM_SECRET_MARKER_7f2c9a"

	tainted := ctxerrors.Wrap(commerr.ErrParseFailed, marker)

	got := classify(tainted)

	assert.Equal(t, http.StatusBadGateway, got.status)
	assert.NotContains(t, got.body.Code, marker)
	assert.NotContains(t, got.body.Message, marker)
}

func TestBadRequestBuildsTheBadRequestEnvelope(t *testing.T) {
	t.Parallel()

	got := badRequest("state is required")

	assert.Equal(t, "state is required", got.Message)
	assert.NotEmpty(t, got.Code)
}
