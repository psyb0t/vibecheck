package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/psyb0t/aichteeteapee/klhayyent"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
)

// statusOverloaded is TypeSafe's documented capacity signal. It is not in
// net/http because it is not a registered status code.
const statusOverloaded = 529

// backoffExponentBase makes each retry wait twice the previous one.
const backoffExponentBase = 2

// statusTooManyRequests aliases the stdlib constant so the retry-reason
// switch reads against one consistent set of names.
const statusTooManyRequests = http.StatusTooManyRequests

// Retry-After can arrive in either unit. The millisecond form is the one
// TypeSafe's own SDKs honor alongside the standard header.
const (
	headerRetryAfter   = "Retry-After"
	headerRetryAfterMS = "retry-after-ms"
)

// failure is one attempt's outcome, already classified.
type failure struct {
	// err is the sentinel-wrapped error to report if this is the last
	// attempt.
	err error

	// statusCode is the HTTP status, or zero when no response arrived.
	statusCode int

	// retryable says whether trying again could plausibly succeed.
	retryable bool

	// retryAfter is the provider's own requested delay, zero when it did
	// not ask for one.
	retryAfter time.Duration
}

// classify turns one attempt's error into a failure with a retry decision.
//
// Caller cancellation and deadline expiry are never retryable: the caller has
// already given up, and retrying would burn provider quota on a response
// nobody will read.
func classify(ctx context.Context, err error) failure {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return failure{
			err:       ctxerrors.Wrap(ctxErr, "provider call cancelled"),
			retryable: false,
		}
	}

	if errors.Is(err, context.Canceled) {
		return failure{
			err: ctxerrors.Wrap(
				context.Canceled, "provider call cancelled",
			),
			retryable: false,
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return failure{
			err: ctxerrors.Wrap(
				commerr.ErrTimeout,
				"provider did not answer within the attempt timeout",
			),
			retryable: true,
		}
	}

	if errors.Is(err, klhayyent.ErrResponseBodyTooLarge) {
		return failure{
			err: ctxerrors.Wrap(
				provider.ErrResponseTooLarge, "provider response refused",
			),
			retryable: false,
		}
	}

	httpErr := &klhayyent.HTTPError{}
	if errors.As(err, &httpErr) && httpErr.Response != nil {
		return classifyStatus(httpErr)
	}

	if decodeFailure, ok := classifyDecode(err); ok {
		return decodeFailure
	}

	// Nothing above matched, so no response ever arrived: a dial failure, a
	// TLS failure, or a connection reset. Those are safe to retry because
	// the request was never processed.
	return failure{
		err: ctxerrors.Wrap(
			commerr.ErrConnectFailed, "provider unreachable",
		),
		retryable: true,
	}
}

// classifyDecode recognises a body that arrived but could not be read as a
// System One response.
//
// The transport succeeded here, so this is the provider breaking its
// contract rather than an outage, and retrying cannot fix it. Without this
// branch the decode error falls through to the unreachable case, gets
// retried against a provider that is answering fine, and is finally
// reported as an outage instead of the protocol fault it is.
func classifyDecode(err error) (failure, bool) {
	var (
		syntaxErr   *json.SyntaxError
		typeErr     *json.UnmarshalTypeError
		invalidErr  *json.InvalidUnmarshalError
		unsupported *json.UnsupportedValueError
	)

	isDecode := errors.As(err, &syntaxErr) ||
		errors.As(err, &typeErr) ||
		errors.As(err, &invalidErr) ||
		errors.As(err, &unsupported) ||
		errors.Is(err, io.ErrUnexpectedEOF)

	if !isDecode {
		return failure{}, false
	}

	return failure{
		err: ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"provider response could not be decoded",
		),
		retryable: false,
	}, true
}

// classifyStatus maps a provider HTTP status to a Vibecheck failure class.
//
// The provider's response body is deliberately not included in any returned
// error: it can echo request content, and request content is exactly what
// Vibecheck must not leak into logs or API responses.
func classifyStatus(httpErr *klhayyent.HTTPError) failure {
	status := httpErr.Response.StatusCode

	if terminal, ok := classifyTerminalStatus(status); ok {
		return terminal
	}

	return classifyRetryableStatus(
		status, parseRetryAfter(httpErr.Response.Header),
	)
}

// classifyTerminalStatus recognizes the statuses that will answer the same
// way however many times the request is repeated. Retrying a bad credential
// or a malformed request only burns the caller's timeout.
func classifyTerminalStatus(status int) (failure, bool) {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return failure{
			err: ctxerrors.Wrapf(
				commerr.ErrNotAuthenticated,
				"provider rejected the credential with status %d", status,
			),
			statusCode: status,
			retryable:  false,
		}, true

	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return failure{
			err: ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"provider rejected the request with status %d", status,
			),
			statusCode: status,
			retryable:  false,
		}, true
	}

	return failure{}, false
}

// classifyRetryableStatus handles the transient statuses. Anything it does
// not recognize is reported as a non-retryable failure: an unknown status is
// not evidence that a second attempt would do better.
func classifyRetryableStatus(status int, retryAfter time.Duration) failure {
	switch {
	case status == http.StatusTooManyRequests:
		return failure{
			err: ctxerrors.Wrap(
				commerr.ErrRateLimited, "provider rate limit reached",
			),
			statusCode: status,
			retryable:  true,
			retryAfter: retryAfter,
		}

	case status == statusOverloaded:
		return failure{
			err: ctxerrors.Wrap(
				provider.ErrOverloaded, "provider reported no spare capacity",
			),
			statusCode: status,
			retryable:  true,
			retryAfter: retryAfter,
		}

	case status >= http.StatusInternalServerError:
		return failure{
			err: ctxerrors.Wrapf(
				commerr.ErrUnavailable,
				"provider failed with status %d", status,
			),
			statusCode: status,
			retryable:  true,
			retryAfter: retryAfter,
		}
	}

	return failure{
		err: ctxerrors.Wrapf(
			commerr.ErrFailed, "provider returned unexpected status %d", status,
		),
		statusCode: status,
		retryable:  false,
	}
}

// parseRetryAfter reads whichever Retry-After form the provider sent and caps
// it, so a provider cannot park an inbound request indefinitely.
//
// Only the delay-seconds form of the standard header is honored. The HTTP-date
// form depends on agreeing with the provider about the current time, and a
// clock skew there would produce a wait this code cannot reason about.
func parseRetryAfter(header http.Header) time.Duration {
	if header == nil {
		return 0
	}

	if raw := header.Get(headerRetryAfterMS); raw != "" {
		if millis, err := strconv.Atoi(raw); err == nil && millis > 0 {
			return capWait(time.Duration(millis) * time.Millisecond)
		}
	}

	raw := header.Get(headerRetryAfter)
	if raw == "" {
		return 0
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0
	}

	return capWait(time.Duration(seconds) * time.Second)
}

func capWait(wait time.Duration) time.Duration {
	return min(wait, MaxRetryAfterWait)
}

// backoffFor returns the delay before the given attempt number, where attempt
// 1 is the first retry. The provider's own Retry-After wins when it sent one,
// because it knows more about its state than an exponential curve does.
func (c *Client) backoffFor(
	attempt int,
	requested time.Duration,
) time.Duration {
	if requested > 0 {
		return requested
	}

	shift := math.Pow(backoffExponentBase, float64(attempt-1))
	delay := time.Duration(float64(c.config.BackoffBase) * shift)

	return min(delay, c.config.BackoffMax)
}
