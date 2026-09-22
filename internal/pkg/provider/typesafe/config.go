// Package typesafe adapts the public TypeSafe client to Vibecheck's provider
// boundary.
package typesafe

import typesafeapi "github.com/psyb0t/vibecheck/pkg/gotypesafe"

const (
	DefaultBaseURL          = typesafeapi.DefaultBaseURL
	DefaultModel            = typesafeapi.DefaultModel
	DefaultTimeout          = typesafeapi.DefaultTimeout
	DefaultMaxResponseBytes = typesafeapi.DefaultMaxResponseBytes
	DefaultConcurrency      = typesafeapi.DefaultConcurrency
	DefaultMaxAttempts      = typesafeapi.DefaultMaxAttempts
	DefaultBackoffBase      = typesafeapi.DefaultBackoffBase
	DefaultBackoffMax       = typesafeapi.DefaultBackoffMax
	DefaultMaxElapsed       = typesafeapi.DefaultMaxElapsed
	MaxRetryAfterWait       = typesafeapi.MaxRetryAfterWait
)

// Config is the public TypeSafe client configuration.
type Config = typesafeapi.Config
