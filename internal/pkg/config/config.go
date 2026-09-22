// Package config parses and validates Vibecheck's deployment configuration.
//
// Everything is read once at startup and never re-read. A bad value fails the
// process before it serves a request, rather than surfacing on the first call
// that happens to touch it.
package config

import (
	"time"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/gonfiguration"
	"github.com/psyb0t/vibecheck/internal/pkg/db"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// Defaults. They err safe: loopback listeners, inline policies off, retention
// off, and bounded request sizes.
const (
	DefaultHTTPListenAddress    = "127.0.0.1:8080"
	DefaultMetricsListenAddress = "127.0.0.1:9091"

	DefaultPolicyDir = "/config/policies"

	DefaultMaxRequestBytes = 1 << 20
	DefaultMaxStateBytes   = 512 << 10
	DefaultMaxFacts        = 128
	DefaultMaxQuestions    = 64
	DefaultMaxPolicyBytes  = 256 << 10

	DefaultMetadataEntries     = 16
	DefaultMetadataValueLength = 256

	DefaultTypeSafeBaseURL       = "https://api.typesafe.ai"
	DefaultTypeSafeModel         = "jev-latest"
	DefaultTypeSafeTimeout       = 10 * time.Second
	DefaultTypeSafeMaxResponse   = 1 << 20
	DefaultTypeSafeConcurrency   = 32
	DefaultTypeSafeMaxAttempts   = 3
	DefaultSQLiteBusyTimeout     = 5 * time.Second
	DefaultPostgresPort          = 5432
	DefaultEvaluationRetention   = 720 * time.Hour
	DefaultIdempotencyRetention  = 24 * time.Hour
	DefaultRetentionCleanupEvery = time.Hour
	DefaultRequestsPerMinute     = 600
	DefaultRateLimitBurst        = 60
	DefaultShutdownDrainGrace    = 5 * time.Second
)

// Config is the full deployment surface. Field order follows .env.example so
// the two stay easy to diff by eye.
type Config struct {
	HTTPListenAddress    string `env:"VIBECHECK_HTTP_LISTEN_ADDRESS"`
	MetricsListenAddress string `env:"VIBECHECK_METRICS_LISTEN_ADDRESS"`
	APIToken             string `env:"VIBECHECK_API_TOKEN"`

	PolicyDir           string `env:"VIBECHECK_POLICY_DIR"`
	AllowInlinePolicies bool   `env:"VIBECHECK_ALLOW_INLINE_POLICIES"`

	MaxRequestBytes int `env:"VIBECHECK_MAX_REQUEST_BYTES"`
	MaxStateBytes   int `env:"VIBECHECK_MAX_STATE_BYTES"`
	MaxFacts        int `env:"VIBECHECK_MAX_FACTS"`
	MaxQuestions    int `env:"VIBECHECK_MAX_QUESTIONS"`
	MaxPolicyBytes  int `env:"VIBECHECK_MAX_POLICY_BYTES"`

	MaxMetadataEntries     int `env:"VIBECHECK_MAX_METADATA_ENTRIES"`
	MaxMetadataValueLength int `env:"VIBECHECK_MAX_METADATA_VALUE_LENGTH"`

	RequestsPerMinute int `env:"VIBECHECK_RATE_LIMIT_PER_MINUTE"`
	RateLimitBurst    int `env:"VIBECHECK_RATE_LIMIT_BURST"`

	TypeSafeBaseURL string        `env:"VIBECHECK_TYPESAFE_BASE_URL"`
	TypeSafeAPIKey  string        `env:"VIBECHECK_TYPESAFE_API_KEY"`
	TypeSafeModel   string        `env:"VIBECHECK_TYPESAFE_MODEL"`
	TypeSafeTimeout time.Duration `env:"VIBECHECK_TYPESAFE_TIMEOUT"`

	TypeSafeMaxResponseBytes int `env:"VIBECHECK_TYPESAFE_MAX_RESPONSE_BYTES"`
	TypeSafeConcurrency      int `env:"VIBECHECK_TYPESAFE_CONCURRENCY"`
	TypeSafeMaxAttempts      int `env:"VIBECHECK_TYPESAFE_MAX_ATTEMPTS"`

	DBDriver            string        `env:"VIBECHECK_DB_DRIVER"`
	DBSQLitePath        string        `env:"VIBECHECK_DB_SQLITE_PATH"`
	DBSQLiteBusyTimeout time.Duration `env:"VIBECHECK_DB_SQLITE_BUSY_TIMEOUT"`
	DBHostname          string        `env:"VIBECHECK_DB_HOSTNAME"`
	DBPort              int           `env:"VIBECHECK_DB_PORT"`
	DBUsername          string        `env:"VIBECHECK_DB_USERNAME"`
	DBPassword          string        `env:"VIBECHECK_DB_PASSWORD"`
	DBName              string        `env:"VIBECHECK_DB_NAME"`
	DBIsSSL             bool          `env:"VIBECHECK_DB_IS_SSL"`

	StoreInputs          bool          `env:"VIBECHECK_STORE_INPUTS"`
	DataKey              string        `env:"VIBECHECK_DATA_KEY"`
	EvaluationRetention  time.Duration `env:"VIBECHECK_EVALUATION_RETENTION"`
	IdempotencyRetention time.Duration `env:"VIBECHECK_IDEMPOTENCY_RETENTION"`

	RetentionCleanupEvery time.Duration `env:"VIBECHECK_RETENTION_CLEANUP_INTERVAL"` //nolint:lll // the env name is fixed by the deployment contract
}

// Parse reads the environment, applies defaults, and validates.
func Parse() (Config, error) {
	setDefaults()

	config := Config{}
	if err := gonfiguration.Parse(&config); err != nil {
		return Config{}, ctxerrors.Wrap(err, "parse vibecheck configuration")
	}

	if err := config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

func setDefaults() {
	gonfiguration.SetDefaults(map[string]any{
		"VIBECHECK_HTTP_LISTEN_ADDRESS":         DefaultHTTPListenAddress,
		"VIBECHECK_METRICS_LISTEN_ADDRESS":      DefaultMetricsListenAddress,
		"VIBECHECK_API_TOKEN":                   "",
		"VIBECHECK_POLICY_DIR":                  DefaultPolicyDir,
		"VIBECHECK_ALLOW_INLINE_POLICIES":       false,
		"VIBECHECK_MAX_REQUEST_BYTES":           DefaultMaxRequestBytes,
		"VIBECHECK_MAX_STATE_BYTES":             DefaultMaxStateBytes,
		"VIBECHECK_MAX_FACTS":                   DefaultMaxFacts,
		"VIBECHECK_MAX_QUESTIONS":               DefaultMaxQuestions,
		"VIBECHECK_MAX_POLICY_BYTES":            DefaultMaxPolicyBytes,
		"VIBECHECK_MAX_METADATA_ENTRIES":        DefaultMetadataEntries,
		"VIBECHECK_MAX_METADATA_VALUE_LENGTH":   DefaultMetadataValueLength,
		"VIBECHECK_RATE_LIMIT_PER_MINUTE":       DefaultRequestsPerMinute,
		"VIBECHECK_RATE_LIMIT_BURST":            DefaultRateLimitBurst,
		"VIBECHECK_TYPESAFE_BASE_URL":           DefaultTypeSafeBaseURL,
		"VIBECHECK_TYPESAFE_API_KEY":            "",
		"VIBECHECK_TYPESAFE_MODEL":              DefaultTypeSafeModel,
		"VIBECHECK_TYPESAFE_TIMEOUT":            DefaultTypeSafeTimeout,
		"VIBECHECK_TYPESAFE_MAX_RESPONSE_BYTES": DefaultTypeSafeMaxResponse,
		"VIBECHECK_TYPESAFE_CONCURRENCY":        DefaultTypeSafeConcurrency,
		"VIBECHECK_TYPESAFE_MAX_ATTEMPTS":       DefaultTypeSafeMaxAttempts,
		"VIBECHECK_DB_DRIVER":                   string(db.DriverSQLite),
		"VIBECHECK_DB_SQLITE_PATH":              "/data/vibecheck.sqlite",
		"VIBECHECK_DB_SQLITE_BUSY_TIMEOUT":      DefaultSQLiteBusyTimeout,
		"VIBECHECK_DB_HOSTNAME":                 "localhost",
		"VIBECHECK_DB_PORT":                     DefaultPostgresPort,
		"VIBECHECK_DB_USERNAME":                 "",
		"VIBECHECK_DB_PASSWORD":                 "",
		"VIBECHECK_DB_NAME":                     "vibecheck",
		"VIBECHECK_DB_IS_SSL":                   true,
		"VIBECHECK_STORE_INPUTS":                false,
		"VIBECHECK_DATA_KEY":                    "",
		"VIBECHECK_EVALUATION_RETENTION":        DefaultEvaluationRetention,
		"VIBECHECK_IDEMPOTENCY_RETENTION":       DefaultIdempotencyRetention,
		"VIBECHECK_RETENTION_CLEANUP_INTERVAL":  DefaultRetentionCleanupEvery,
	})
}

// Validate rejects a configuration that cannot produce a working service.
//
// The API key is deliberately not required here. A deployment may start
// without one, serve /healthz, and report the gap through readiness rather
// than crash-looping on a missing secret.
func (c Config) Validate() error {
	if err := c.validateListeners(); err != nil {
		return err
	}

	if err := c.validateLimits(); err != nil {
		return err
	}

	if err := c.validateDatabase(); err != nil {
		return err
	}

	return c.validateRetention()
}

func (c Config) validateListeners() error {
	if c.HTTPListenAddress == "" {
		return ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"VIBECHECK_HTTP_LISTEN_ADDRESS",
		)
	}

	if c.MetricsListenAddress == "" {
		return ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"VIBECHECK_METRICS_LISTEN_ADDRESS",
		)
	}

	if c.HTTPListenAddress == c.MetricsListenAddress {
		//nolint:lll // one error string, splitting it would change the text
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue,
			"the metrics listener must not share an address with the public API",
		)
	}

	return nil
}

func (c Config) validateLimits() error {
	positive := map[string]int{
		"VIBECHECK_MAX_REQUEST_BYTES":           c.MaxRequestBytes,
		"VIBECHECK_MAX_STATE_BYTES":             c.MaxStateBytes,
		"VIBECHECK_MAX_FACTS":                   c.MaxFacts,
		"VIBECHECK_MAX_QUESTIONS":               c.MaxQuestions,
		"VIBECHECK_MAX_POLICY_BYTES":            c.MaxPolicyBytes,
		"VIBECHECK_MAX_METADATA_ENTRIES":        c.MaxMetadataEntries,
		"VIBECHECK_MAX_METADATA_VALUE_LENGTH":   c.MaxMetadataValueLength,
		"VIBECHECK_TYPESAFE_MAX_RESPONSE_BYTES": c.TypeSafeMaxResponseBytes,
		"VIBECHECK_TYPESAFE_CONCURRENCY":        c.TypeSafeConcurrency,
		"VIBECHECK_TYPESAFE_MAX_ATTEMPTS":       c.TypeSafeMaxAttempts,
		"VIBECHECK_RATE_LIMIT_PER_MINUTE":       c.RequestsPerMinute,
		"VIBECHECK_RATE_LIMIT_BURST":            c.RateLimitBurst,
	}

	for name, value := range positive {
		if value <= 0 {
			return ctxerrors.Wrapf(
				commerr.ErrInvalidValue,
				"%s must be positive, got %d",
				name,
				value,
			)
		}
	}

	if c.MaxStateBytes > c.MaxRequestBytes {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue,
			"VIBECHECK_MAX_STATE_BYTES cannot exceed "+
				"VIBECHECK_MAX_REQUEST_BYTES",
		)
	}

	if c.MaxQuestions > policy.MaxQuestions {
		return ctxerrors.Wrapf(
			commerr.ErrInvalidValue,
			"VIBECHECK_MAX_QUESTIONS cannot exceed %d",
			policy.MaxQuestions,
		)
	}

	if c.TypeSafeTimeout <= 0 {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue,
			"VIBECHECK_TYPESAFE_TIMEOUT must be positive",
		)
	}

	return nil
}

func (c Config) validateDatabase() error {
	driver := db.Driver(c.DBDriver)
	if !driver.IsValid() {
		return ctxerrors.Wrapf(
			commerr.ErrInvalidValue,
			"VIBECHECK_DB_DRIVER must be %q or %q, got %q",
			db.DriverSQLite, db.DriverPostgres, c.DBDriver,
		)
	}

	if driver == db.DriverSQLite {
		if c.DBSQLitePath == "" {
			return ctxerrors.Wrap(
				commerr.ErrRequiredConfigValueNotSet,
				"VIBECHECK_DB_SQLITE_PATH",
			)
		}

		return nil
	}

	required := map[string]string{
		"VIBECHECK_DB_HOSTNAME": c.DBHostname,
		"VIBECHECK_DB_USERNAME": c.DBUsername,
		"VIBECHECK_DB_PASSWORD": c.DBPassword,
		"VIBECHECK_DB_NAME":     c.DBName,
	}

	for name, value := range required {
		if value == "" {
			return ctxerrors.Wrap(commerr.ErrRequiredConfigValueNotSet, name)
		}
	}

	if c.DBPort <= 0 {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue, "VIBECHECK_DB_PORT must be positive",
		)
	}

	return nil
}

// validateRetention enforces the one rule that matters most here: retention
// cannot be turned on without a key. Storing raw input in the clear must not
// be reachable by flipping a single boolean.
func (c Config) validateRetention() error {
	if c.StoreInputs && c.DataKey == "" {
		return ctxerrors.Wrap(
			commerr.ErrRequiredConfigValueNotSet,
			"VIBECHECK_STORE_INPUTS is on, so VIBECHECK_DATA_KEY is required",
		)
	}

	if c.EvaluationRetention < 0 || c.IdempotencyRetention < 0 {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue, "retention windows cannot be negative",
		)
	}

	if c.RetentionCleanupEvery <= 0 {
		return ctxerrors.Wrap(
			commerr.ErrInvalidValue,
			"VIBECHECK_RETENTION_CLEANUP_INTERVAL must be positive",
		)
	}

	return nil
}

// DBConfig projects the database settings onto the db package's own shape.
func (c Config) DBConfig() db.Config {
	return db.Config{
		Driver:            db.Driver(c.DBDriver),
		SQLitePath:        c.DBSQLitePath,
		SQLiteBusyTimeout: c.DBSQLiteBusyTimeout,
		Postgres: db.PostgresConfig{
			Hostname: c.DBHostname,
			Port:     c.DBPort,
			Username: c.DBUsername,
			Password: c.DBPassword,
			Database: c.DBName,
			IsSSL:    c.DBIsSSL,
		},
	}
}

// AuthEnabled reports whether a bearer token is configured. With no token the
// service is trusted-loopback only.
func (c Config) AuthEnabled() bool {
	return c.APIToken != ""
}
