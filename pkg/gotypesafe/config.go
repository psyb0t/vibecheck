package typesafe

import (
	"net/http"
	"time"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"
)

const (
	DefaultTimeout          = 10 * time.Second
	DefaultMaxResponseBytes = 1 << 20
	DefaultConcurrency      = 32
	DefaultMaxAttempts      = 3
	DefaultBackoffBase      = 200 * time.Millisecond
	DefaultBackoffMax       = 5 * time.Second
	DefaultMaxElapsed       = 30 * time.Second
	MaxRetryAfterWait       = 30 * time.Second
)

// Config controls the bounded HTTP client.
type Config struct {
	BaseURL          string
	APIKey           string
	DefaultModel     string
	Timeout          time.Duration
	MaxResponseBytes int64
	Concurrency      int
	MaxAttempts      int
	BackoffBase      time.Duration
	BackoffMax       time.Duration
	MaxElapsed       time.Duration
	HTTPClient       *http.Client
}

func (c Config) withDefaults() Config {
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

func (c Config) validate() error {
	if c.BaseURL == "" {
		return ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"TypeSafe base URL",
		)
	}

	if c.BackoffMax < c.BackoffBase {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue,
			"TypeSafe backoff maximum is below its base delay",
		)
	}

	return nil
}
