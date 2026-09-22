package typesafe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

const (
	statusOverloaded       = 529
	backoffExponentBase    = 2
	headerRetryAfter       = "Retry-After"
	headerRetryAfterMillis = "retry-after-ms"
)

type failure struct {
	err        error
	statusCode int
	retryable  bool
	retryAfter time.Duration
}

// HTTPClient is a concurrency-safe, bounded TypeSafe client.
type HTTPClient struct {
	config    Config
	generated ClientInterface
	slots     chan struct{}
	observe   AttemptObserver
}

// Option adjusts an HTTPClient during construction.
type Option func(*HTTPClient)

// WithAttemptObserver installs a non-blocking per-attempt observer.
func WithAttemptObserver(observe AttemptObserver) Option {
	return func(client *HTTPClient) {
		client.observe = observe
	}
}

// New builds a bounded TypeSafe HTTP client.
func New(config Config, options ...Option) (*HTTPClient, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: config.Timeout}
	}

	generated, err := NewClient(
		config.BaseURL,
		WithHTTPClient(httpClient),
		WithRequestEditorFn(func(
			_ context.Context,
			request *http.Request,
		) error {
			request.Header.Set("Authorization", "Bearer "+config.APIKey)

			return nil
		}),
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build generated TypeSafe client")
	}

	client := &HTTPClient{
		config:    config,
		generated: generated,
		slots:     make(chan struct{}, config.Concurrency),
	}
	for _, option := range options {
		option(client)
	}

	return client, nil
}

// Evaluate sends every question in one System One request.
func (c *HTTPClient) Evaluate(
	ctx context.Context,
	request Request,
) (Response, error) {
	if c.config.APIKey == "" {
		return Response{}, ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"TypeSafe API key",
		)
	}

	body, err := buildRequest(request, c.config.DefaultModel)
	if err != nil {
		return Response{}, err
	}

	release, err := c.acquireSlot(ctx)
	if err != nil {
		return Response{}, err
	}
	defer release()

	return c.evaluateWithRetries(ctx, body)
}

func (c *HTTPClient) evaluateWithRetries(
	ctx context.Context,
	body SystemoneV1SystemonePostJSONRequestBody,
) (Response, error) {
	startedAt := time.Now()
	attempts := make([]Attempt, 0, c.config.MaxAttempts)

	var lastFailure failure

	for number := 1; number <= c.config.MaxAttempts; number++ {
		response, attempt, attemptFailure := c.evaluateAttempt(
			ctx,
			number,
			body,
		)
		attempts = append(attempts, attempt)
		c.record(attempt)

		if attemptFailure == nil {
			response.Attempts = attempts
			response.Duration = time.Since(startedAt)

			return response, nil
		}

		lastFailure = *attemptFailure
		if !c.shouldRetry(ctx, lastFailure, number, startedAt) {
			break
		}

		if err := c.waitBeforeRetry(
			ctx,
			number,
			lastFailure,
			startedAt,
		); err != nil {
			return Response{}, err
		}
	}

	return Response{}, ctxerrors.Wrapf(
		lastFailure.err,
		"TypeSafe call failed after %d attempts",
		len(attempts),
	)
}

// Models returns every model and alias available to the credential.
func (c *HTTPClient) Models(ctx context.Context) ([]ModelMetadata, error) {
	if c.config.APIKey == "" {
		return nil, ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"TypeSafe API key",
		)
	}

	release, err := c.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	// readResponse owns and closes the body.
	response, err := c.generated.ModelsV1V1ModelsGet(ctx) //nolint:bodyclose
	if err != nil {
		return nil, classifyTransport(ctx, err).err
	}

	body, err := c.readResponse(response)
	if err != nil {
		return nil, err
	}

	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, classifyStatus(response).err
	}

	decoded := ModelMetadataList{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"decode TypeSafe models response",
		)
	}

	return decoded.Models, nil
}

func (c *HTTPClient) evaluateAttempt(
	ctx context.Context,
	number int,
	body SystemoneV1SystemonePostJSONRequestBody,
) (Response, Attempt, *failure) {
	startedAt := time.Now()
	// readResponse owns and closes the body. The generated call returns it.
	//nolint:bodyclose
	httpResponse, err := c.generated.SystemoneV1SystemonePost(
		ctx,
		body,
	)
	attempt := Attempt{Number: number, Duration: time.Since(startedAt)}

	if err != nil {
		attemptFailure := classifyTransport(ctx, err)
		attempt.Err = attemptFailure.err

		return Response{}, attempt, &attemptFailure
	}

	attempt.StatusCode = httpResponse.StatusCode

	responseBody, err := c.readResponse(httpResponse)
	if err != nil {
		attempt.Err = err

		return Response{}, attempt, &failure{
			err:        err,
			statusCode: attempt.StatusCode,
		}
	}

	if httpResponse.StatusCode < http.StatusOK ||
		httpResponse.StatusCode >= http.StatusMultipleChoices {
		attemptFailure := classifyStatus(httpResponse)
		attempt.Err = attemptFailure.err

		return Response{}, attempt, &attemptFailure
	}

	response, err := decodeSystemOneResponse(responseBody)
	if err != nil {
		parseErr := ctxerrors.Wrap(
			err,
			"decode TypeSafe response",
		)
		attempt.Err = parseErr

		return Response{}, attempt, &failure{
			err:        parseErr,
			statusCode: attempt.StatusCode,
		}
	}

	return response, attempt, nil
}

func decodeSystemOneResponse(body []byte) (Response, error) {
	decoded := SystemOneResponse{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Response{}, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"decode TypeSafe System One response",
		)
	}

	return normalizeResponse(decoded)
}

func (c *HTTPClient) readResponse(response *http.Response) ([]byte, error) {
	defer func() {
		_ = response.Body.Close() // response data has already been consumed
	}()

	limited := io.LimitReader(response.Body, c.config.MaxResponseBytes+1)

	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "read TypeSafe response")
	}

	if int64(len(body)) > c.config.MaxResponseBytes {
		return nil, ctxerrors.Wrap(
			ErrResponseTooLarge,
			"refuse oversized TypeSafe response",
		)
	}

	return body, nil
}

func (c *HTTPClient) acquireSlot(ctx context.Context) (func(), error) {
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, nil
	case <-ctx.Done():
		return nil, ctxerrors.Wrap(
			ctx.Err(),
			"wait for a TypeSafe concurrency slot",
		)
	}
}

func (c *HTTPClient) shouldRetry(
	ctx context.Context,
	attemptFailure failure,
	number int,
	startedAt time.Time,
) bool {
	return attemptFailure.retryable &&
		number < c.config.MaxAttempts &&
		ctx.Err() == nil &&
		time.Since(startedAt) < c.config.MaxElapsed
}

func (c *HTTPClient) waitBeforeRetry(
	ctx context.Context,
	number int,
	attemptFailure failure,
	startedAt time.Time,
) error {
	delay := c.backoffFor(number, attemptFailure.retryAfter)
	if time.Since(startedAt)+delay >= c.config.MaxElapsed {
		return ctxerrors.Wrap(
			commerr.ErrTimeout,
			"TypeSafe retry budget exhausted",
		)
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctxerrors.Wrap(ctx.Err(), "TypeSafe retry cancelled")
	}
}

func (c *HTTPClient) backoffFor(
	number int,
	retryAfter time.Duration,
) time.Duration {
	delay := min(max(retryAfter, min(time.Duration(
		float64(c.config.BackoffBase)*
			math.Pow(backoffExponentBase, float64(number-1)),
	), c.config.BackoffMax)), MaxRetryAfterWait)

	return delay
}

func (c *HTTPClient) record(attempt Attempt) {
	if c.observe != nil {
		c.observe(attempt)
	}
}

func classifyTransport(ctx context.Context, err error) failure {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return failure{err: ctxerrors.Wrap(ctxErr, "TypeSafe call cancelled")}
	}

	if errors.Is(err, context.Canceled) {
		return failure{err: ctxerrors.Wrap(
			context.Canceled,
			"TypeSafe call cancelled",
		)}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return failure{
			err: ctxerrors.Wrap(
				commerr.ErrTimeout,
				"TypeSafe attempt timed out",
			),
			retryable: true,
		}
	}

	return failure{
		err: ctxerrors.Wrap(
			commerr.ErrConnectFailed,
			"TypeSafe is unreachable",
		),
		retryable: true,
	}
}

func classifyStatus(response *http.Response) failure {
	statusCode := response.StatusCode
	base := failure{statusCode: statusCode}

	switch statusCode {
	case http.StatusUnauthorized:
		base.err = ctxerrors.Wrap(
			commerr.ErrNotAuthenticated,
			"TypeSafe rejected the credential",
		)
	case http.StatusForbidden:
		base.err = ctxerrors.Wrap(
			commerr.ErrPermissionDenied,
			"TypeSafe denied the request",
		)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		base.err = ctxerrors.Wrap(
			commerr.ErrValidationFailed,
			"TypeSafe rejected the request",
		)
	case http.StatusTooManyRequests:
		base.err = ctxerrors.Wrap(
			commerr.ErrRateLimited,
			"TypeSafe rate limited the request",
		)
		base.retryable = true
	case statusOverloaded:
		base.err = ctxerrors.Wrap(
			ErrOverloaded,
			"TypeSafe reported no spare capacity",
		)
		base.retryable = true
	default:
		if statusCode >= http.StatusInternalServerError {
			base.err = ctxerrors.Wrap(
				commerr.ErrFetchFailed,
				"TypeSafe server failed",
			)
			base.retryable = true
		} else {
			base.err = ctxerrors.Wrap(
				commerr.ErrFetchFailed,
				"TypeSafe returned an unexpected status",
			)
		}
	}

	base.retryAfter = parseRetryAfter(response.Header)

	return base
}

func parseRetryAfter(headers http.Header) time.Duration {
	if milliseconds, err := strconv.ParseInt(
		headers.Get(headerRetryAfterMillis),
		10,
		64,
	); err == nil && milliseconds > 0 {
		return time.Duration(milliseconds) * time.Millisecond
	}

	value := headers.Get(headerRetryAfter)
	if seconds, err := strconv.ParseInt(
		value,
		10,
		64,
	); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	if retryAt, err := http.ParseTime(value); err == nil {
		return max(time.Until(retryAt), 0)
	}

	return 0
}
