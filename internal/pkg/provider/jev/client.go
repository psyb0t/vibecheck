package jev

import (
	"context"
	"time"

	"github.com/psyb0t/aichteeteapee/klhayyent"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
)

// Client is the TypeSafe System One provider adapter.
//
// It is safe for concurrent use and bounds itself three ways: one attempt
// cannot outlast the configured timeout, the retry series cannot outlast the
// configured elapsed budget, and no more than the configured number of
// requests are ever in flight across the whole process.
type Client struct {
	config Config
	http   *klhayyent.Client

	// slots is a counting semaphore. A buffered channel rather than a
	// library type so that waiting for a slot stays cancellable by the
	// caller's context.
	slots chan struct{}

	// observe receives one record per physical attempt. It is how metrics
	// get attached without this package importing a metrics library.
	observe AttemptObserver
}

// AttemptObserver is called once per physical attempt, successful or not. It
// must not block: it runs on the request path.
type AttemptObserver func(attempt provider.Attempt)

// Option adjusts a client at construction.
type Option func(*Client)

// WithAttemptObserver installs a per-attempt callback, typically a metrics
// recorder.
func WithAttemptObserver(observe AttemptObserver) Option {
	return func(c *Client) {
		c.observe = observe
	}
}

// New builds a client from validated configuration.
func New(config Config, options ...Option) (*Client, error) {
	config = config.WithDefaults()

	if err := config.Validate(); err != nil {
		return nil, err
	}

	httpClient, err := klhayyent.New(
		config.BaseURL,
		klhayyent.WithTimeout(config.Timeout),
		klhayyent.WithMaxResponseBodySize(config.MaxResponseBytes),
		klhayyent.WithDependencyName(dependencyName),
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build provider HTTP client")
	}

	client := &Client{
		config: config,
		http:   httpClient,
		slots:  make(chan struct{}, config.Concurrency),
	}

	for _, option := range options {
		option(client)
	}

	return client, nil
}

// Evaluate sends every question in one request and returns typed answers.
//
// It retries only states where a retry could plausibly succeed: a connection
// that never produced a response, a rate limit, an overload signal, and a
// server-side failure. Authentication, request validation, response-size, and
// caller-cancellation failures are returned on the first attempt.
func (c *Client) Evaluate(
	ctx context.Context,
	request provider.Request,
) (provider.Response, error) {
	if c.config.APIKey == "" {
		return provider.Response{}, ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet, "provider API key",
		)
	}

	body, err := buildRequest(request, c.config.DefaultModel)
	if err != nil {
		return provider.Response{}, err
	}

	release, err := c.acquireSlot(ctx)
	if err != nil {
		return provider.Response{}, err
	}
	defer release()

	return c.runAttempts(ctx, body)
}

// acquireSlot blocks until a concurrency slot is free or the caller gives up.
func (c *Client) acquireSlot(ctx context.Context) (func(), error) {
	select {
	case c.slots <- struct{}{}:
		return func() { <-c.slots }, nil
	case <-ctx.Done():
		return nil, ctxerrors.Wrap(
			ctx.Err(), "gave up waiting for a provider concurrency slot",
		)
	}
}

// runAttempts drives the retry series and records every attempt.
func (c *Client) runAttempts(
	ctx context.Context,
	body systemOneRequest,
) (provider.Response, error) {
	startedAt := time.Now()
	attempts := make([]provider.Attempt, 0, c.config.MaxAttempts)

	var lastFailure failure

	for number := 1; number <= c.config.MaxAttempts; number++ {
		response, attempt, attemptFailure := c.attempt(ctx, number, body)
		attempts = append(attempts, attempt)
		c.record(attempt)

		if attemptFailure == nil {
			response.Attempts = attempts
			response.Duration = time.Since(startedAt)

			return response, nil
		}

		lastFailure = *attemptFailure

		if !c.shouldRetry(ctx, *attemptFailure, number, startedAt) {
			break
		}

		if err := c.waitBeforeRetry(
			ctx, number, *attemptFailure, startedAt,
		); err != nil {
			return provider.Response{}, err
		}
	}

	return provider.Response{}, ctxerrors.Wrapf(
		lastFailure.err,
		"provider call failed after %d attempts",
		len(attempts),
	)
}

// attempt performs one physical request.
func (c *Client) attempt(
	ctx context.Context,
	number int,
	body systemOneRequest,
) (provider.Response, provider.Attempt, *failure) {
	startedAt := time.Now()

	decoded := systemOneResponse{}

	httpResponse, err := c.http.Post(
		ctx,
		SystemOnePath,
		body,
		&decoded,
		klhayyent.WithBearerToken(c.config.APIKey),
	)

	attempt := provider.Attempt{
		Number:   number,
		Duration: time.Since(startedAt),
	}

	if httpResponse != nil {
		attempt.StatusCode = httpResponse.StatusCode
	}

	if err != nil {
		attemptFailure := classify(ctx, err)
		if attemptFailure.statusCode != 0 {
			attempt.StatusCode = attemptFailure.statusCode
		}

		attempt.Err = attemptFailure.err

		return provider.Response{}, attempt, &attemptFailure
	}

	return interpretResponse(decoded, attempt)
}

// interpretResponse turns a decoded success body into a provider response.
//
// Every failure it reports is non-retryable. The transport already succeeded,
// so a body that does not satisfy the contract is the provider breaking its
// own contract, and a second attempt fetches the same broken shape again.
func interpretResponse(
	decoded systemOneResponse,
	attempt provider.Attempt,
) (provider.Response, provider.Attempt, *failure) {
	answers, err := decodeAnswers(decoded)
	if err != nil {
		attempt.Err = err

		return provider.Response{}, attempt, &failure{
			err:        err,
			statusCode: attempt.StatusCode,
			retryable:  false,
		}
	}

	if decoded.Model == "" {
		err := ctxerrors.Wrap(
			commerr.ErrParseFailed, "provider response names no model",
		)
		attempt.Err = err

		return provider.Response{}, attempt, &failure{
			err:        err,
			statusCode: attempt.StatusCode,
			retryable:  false,
		}
	}

	return provider.Response{
		Model:   decoded.Model,
		Answers: answers,
		Usage: provider.Usage{
			InputTokens:  decoded.Usage.InputTokens,
			OutputTokens: decoded.Usage.OutputTokens,
		},
	}, attempt, nil
}

// shouldRetry decides whether another attempt is allowed, given the failure
// class, the attempt budget, and the elapsed budget.
func (c *Client) shouldRetry(
	ctx context.Context,
	attemptFailure failure,
	number int,
	startedAt time.Time,
) bool {
	if !attemptFailure.retryable {
		return false
	}

	if number >= c.config.MaxAttempts {
		return false
	}

	if ctx.Err() != nil {
		return false
	}

	return time.Since(startedAt) < c.config.MaxElapsed
}

// waitBeforeRetry sleeps for the backoff delay, returning early if the caller
// cancels or the elapsed budget would be exceeded by waiting.
func (c *Client) waitBeforeRetry(
	ctx context.Context,
	number int,
	attemptFailure failure,
	startedAt time.Time,
) error {
	delay := c.backoffFor(number, attemptFailure.retryAfter)

	remaining := c.config.MaxElapsed - time.Since(startedAt)
	if delay > remaining {
		delay = remaining
	}

	if delay <= 0 {
		return nil
	}

	logger := ctxscope.GetLogger(ctx)
	logger.Debug(
		"retrying provider call",
		"attempt", number,
		"delay_ms", delay.Milliseconds(),
		"reason", retryReason(attemptFailure),
	)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctxerrors.Wrap(ctx.Err(), "provider retry cancelled")
	}
}

// retryReason is a stable machine-readable value for the retry log line. It
// never contains provider response content.
func retryReason(attemptFailure failure) string {
	if attemptFailure.statusCode == 0 {
		return "no_response"
	}

	switch attemptFailure.statusCode {
	case statusTooManyRequests:
		return "rate_limited"
	case statusOverloaded:
		return "overloaded"
	}

	return "server_error"
}

func (c *Client) record(attempt provider.Attempt) {
	if c.observe == nil {
		return
	}

	c.observe(attempt)
}
