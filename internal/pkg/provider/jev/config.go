// Package jev speaks the TypeSafe System One protocol.
//
// It is the only place in Vibecheck that knows TypeSafe exists. Everything it
// returns is expressed in the transport-free types of the parent provider
// package, so swapping the provider means adding a sibling package rather
// than touching the evaluator, the core, or either transport.
//
// There is no chat-completion compatibility layer here. Choice, Score, and
// Noul are not a chat contract, and pretending otherwise would mean inventing
// a translation nobody asked for.
package jev

import (
	"time"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

// Protocol constants for the System One endpoint.
const (
	// DefaultBaseURL is the official API origin. It is trusted startup
	// configuration and never request input, because a caller-supplied base
	// URL would turn every evaluation into a credential-leaking SSRF.
	DefaultBaseURL = "https://api.typesafe.ai"

	// SystemOnePath is the evaluation endpoint, relative to the base URL.
	SystemOnePath = "/v1/systemone"

	// DefaultModel is the stable alias. The response reports the versioned
	// model that actually answered, and that is what gets recorded.
	DefaultModel = "jev-latest"
)

// Defaults for the bounded client. Every one of these is overridable by
// deployment configuration.
const (
	DefaultTimeout          = 10 * time.Second
	DefaultMaxResponseBytes = 1 << 20
	DefaultConcurrency      = 32
	DefaultMaxAttempts      = 3
	DefaultBackoffBase      = 200 * time.Millisecond
	DefaultBackoffMax       = 5 * time.Second
	DefaultMaxElapsed       = 30 * time.Second

	// MaxRetryAfterWait caps how long a Retry-After header can park a
	// request. A provider asking for ten minutes must not hold an inbound
	// HTTP request open for ten minutes.
	MaxRetryAfterWait = 30 * time.Second
)

// dependencyName labels this client in the structured logs the HTTP layer
// emits.
const dependencyName = "typesafe_jev"

// Config is the adapter's deployment configuration. It is validated once at
// startup and never changes afterwards.
type Config struct {
	// BaseURL is the trusted provider origin.
	BaseURL string

	// APIKey is the bearer credential. It is never logged and never
	// included in an error returned to a caller.
	APIKey string

	// DefaultModel is used when a policy does not name one.
	DefaultModel string

	// Timeout bounds one physical attempt, not the whole retry series.
	Timeout time.Duration

	// MaxResponseBytes bounds the response body before it is decoded.
	MaxResponseBytes int64

	// Concurrency caps simultaneous in-flight provider requests across the
	// whole process.
	Concurrency int

	// MaxAttempts bounds the retry series, counting the first try.
	MaxAttempts int

	// BackoffBase and BackoffMax bound the exponential delay between
	// attempts.
	BackoffBase time.Duration
	BackoffMax  time.Duration

	// MaxElapsed bounds the whole retry series. It exists so a chain of
	// individually-bounded attempts cannot add up to an unbounded wait.
	MaxElapsed time.Duration
}

// WithDefaults returns a copy with every unset field filled in.
func (c Config) WithDefaults() Config {
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}

	if c.DefaultModel == "" {
		c.DefaultModel = DefaultModel
	}

	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}

	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = DefaultMaxResponseBytes
	}

	if c.Concurrency <= 0 {
		c.Concurrency = DefaultConcurrency
	}

	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}

	if c.BackoffBase <= 0 {
		c.BackoffBase = DefaultBackoffBase
	}

	if c.BackoffMax <= 0 {
		c.BackoffMax = DefaultBackoffMax
	}

	if c.MaxElapsed <= 0 {
		c.MaxElapsed = DefaultMaxElapsed
	}

	return c
}

// Validate rejects a configuration that cannot produce a working client.
//
// An empty API key is allowed here and refused at call time instead, so a
// deployment can start, serve health checks, and report a clear readiness
// failure rather than crash-looping on a missing secret.
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet, "provider base URL",
		)
	}

	if c.BackoffMax < c.BackoffBase {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue,
			"provider backoff maximum is below the base delay",
		)
	}

	return nil
}
