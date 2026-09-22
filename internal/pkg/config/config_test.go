package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/config"
	"github.com/psyb0t/vibecheck/internal/pkg/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vibecheckEnvKeys is every env key Config binds. Kept in one place so the
// env-clearing helper never drifts from what config.go actually reads.
var vibecheckEnvKeys = []string{
	"VIBECHECK_HTTP_LISTEN_ADDRESS",
	"VIBECHECK_METRICS_LISTEN_ADDRESS",
	"VIBECHECK_API_TOKEN",
	"VIBECHECK_POLICY_DIR",
	"VIBECHECK_ALLOW_INLINE_POLICIES",
	"VIBECHECK_MAX_REQUEST_BYTES",
	"VIBECHECK_MAX_STATE_BYTES",
	"VIBECHECK_MAX_FACTS",
	"VIBECHECK_MAX_QUESTIONS",
	"VIBECHECK_MAX_POLICY_BYTES",
	"VIBECHECK_MAX_METADATA_ENTRIES",
	"VIBECHECK_MAX_METADATA_VALUE_LENGTH",
	"VIBECHECK_RATE_LIMIT_PER_MINUTE",
	"VIBECHECK_RATE_LIMIT_BURST",
	"VIBECHECK_TYPESAFE_BASE_URL",
	"VIBECHECK_TYPESAFE_API_KEY",
	"VIBECHECK_TYPESAFE_MODEL",
	"VIBECHECK_TYPESAFE_TIMEOUT",
	"VIBECHECK_TYPESAFE_MAX_RESPONSE_BYTES",
	"VIBECHECK_TYPESAFE_CONCURRENCY",
	"VIBECHECK_TYPESAFE_MAX_ATTEMPTS",
	"VIBECHECK_DB_DRIVER",
	"VIBECHECK_DB_SQLITE_PATH",
	"VIBECHECK_DB_SQLITE_BUSY_TIMEOUT",
	"VIBECHECK_DB_HOSTNAME",
	"VIBECHECK_DB_PORT",
	"VIBECHECK_DB_USERNAME",
	"VIBECHECK_DB_PASSWORD",
	"VIBECHECK_DB_NAME",
	"VIBECHECK_DB_IS_SSL",
	"VIBECHECK_STORE_INPUTS",
	"VIBECHECK_DATA_KEY",
	"VIBECHECK_EVALUATION_RETENTION",
	"VIBECHECK_IDEMPOTENCY_RETENTION",
	"VIBECHECK_RETENTION_CLEANUP_INTERVAL",
}

// clearVibecheckEnv unsets every VIBECHECK_ key for the duration of the
// test and restores whatever was there before. It mutates real process
// env, same as t.Setenv, so callers must not run in parallel.
func clearVibecheckEnv(t *testing.T) {
	t.Helper()

	for _, key := range vibecheckEnvKeys {
		original, wasSet := os.LookupEnv(key)
		require.NoError(t, os.Unsetenv(key))

		t.Cleanup(func() {
			if !wasSet {
				return
			}

			require.NoError(t, os.Setenv(key, original))
		})
	}
}

// validBaseConfig is a fully valid, sqlite-backed configuration. It is
// exactly what Parse applies as defaults, and it doubles as the baseline
// that Validate rejection cases mutate one field at a time.
func validBaseConfig() config.Config {
	return config.Config{
		HTTPListenAddress:    "127.0.0.1:8080",
		MetricsListenAddress: "127.0.0.1:9091",
		APIToken:             "",

		PolicyDir:           "/config/policies",
		AllowInlinePolicies: false,

		MaxRequestBytes: 1 << 20,
		MaxStateBytes:   512 << 10,
		MaxFacts:        128,
		MaxQuestions:    64,
		MaxPolicyBytes:  256 << 10,

		MaxMetadataEntries:     16,
		MaxMetadataValueLength: 256,

		RequestsPerMinute: 600,
		RateLimitBurst:    60,

		TypeSafeBaseURL:          "https://api.typesafe.ai",
		TypeSafeAPIKey:           "",
		TypeSafeModel:            "jev-latest",
		TypeSafeTimeout:          10 * time.Second,
		TypeSafeMaxResponseBytes: 1 << 20,
		TypeSafeConcurrency:      32,
		TypeSafeMaxAttempts:      3,

		DBDriver:            string(db.DriverSQLite),
		DBSQLitePath:        "/data/vibecheck.sqlite",
		DBSQLiteBusyTimeout: 5 * time.Second,
		DBHostname:          "localhost",
		DBPort:              5432,
		DBUsername:          "",
		DBPassword:          "",
		DBName:              "vibecheck",
		DBIsSSL:             true,

		StoreInputs:          false,
		DataKey:              "",
		EvaluationRetention:  720 * time.Hour,
		IdempotencyRetention: 24 * time.Hour,

		RetentionCleanupEvery: time.Hour,
	}
}

// validPostgresConfig is a fully valid postgres-backed configuration, used
// by the postgres-specific Validate and DBConfig cases.
func validPostgresConfig() config.Config {
	cfg := validBaseConfig()
	cfg.DBDriver = string(db.DriverPostgres)
	cfg.DBHostname = "db.internal"
	cfg.DBUsername = "vc_user"
	cfg.DBPassword = "vc_pass"
	cfg.DBName = "vibecheck_test"
	cfg.DBPort = 6543

	return cfg
}

func TestParseAppliesDefaults(t *testing.T) {
	clearVibecheckEnv(t)

	got, err := config.Parse()
	require.NoError(t, err)

	assert.Equal(t, validBaseConfig(), got)
}

func TestParseReadsEnvOverrides(t *testing.T) {
	clearVibecheckEnv(t)

	overrides := map[string]string{
		"VIBECHECK_HTTP_LISTEN_ADDRESS":         "0.0.0.0:9000",
		"VIBECHECK_METRICS_LISTEN_ADDRESS":      "0.0.0.0:9001",
		"VIBECHECK_API_TOKEN":                   "test-token",
		"VIBECHECK_POLICY_DIR":                  "/tmp/policies",
		"VIBECHECK_ALLOW_INLINE_POLICIES":       "true",
		"VIBECHECK_MAX_REQUEST_BYTES":           "2097152",
		"VIBECHECK_MAX_STATE_BYTES":             "1048576",
		"VIBECHECK_MAX_FACTS":                   "256",
		"VIBECHECK_MAX_QUESTIONS":               "32",
		"VIBECHECK_MAX_POLICY_BYTES":            "524288",
		"VIBECHECK_MAX_METADATA_ENTRIES":        "32",
		"VIBECHECK_MAX_METADATA_VALUE_LENGTH":   "512",
		"VIBECHECK_RATE_LIMIT_PER_MINUTE":       "1200",
		"VIBECHECK_RATE_LIMIT_BURST":            "120",
		"VIBECHECK_TYPESAFE_BASE_URL":           "https://ts.example.test",
		"VIBECHECK_TYPESAFE_API_KEY":            "typesafe-key",
		"VIBECHECK_TYPESAFE_MODEL":              "jev-test",
		"VIBECHECK_TYPESAFE_TIMEOUT":            "20s",
		"VIBECHECK_TYPESAFE_MAX_RESPONSE_BYTES": "2097152",
		"VIBECHECK_TYPESAFE_CONCURRENCY":        "64",
		"VIBECHECK_TYPESAFE_MAX_ATTEMPTS":       "5",
		"VIBECHECK_DB_DRIVER":                   "postgres",
		"VIBECHECK_DB_SQLITE_PATH":              "/tmp/test.sqlite",
		"VIBECHECK_DB_SQLITE_BUSY_TIMEOUT":      "2s",
		"VIBECHECK_DB_HOSTNAME":                 "db.internal",
		"VIBECHECK_DB_PORT":                     "6543",
		"VIBECHECK_DB_USERNAME":                 "vc_user",
		"VIBECHECK_DB_PASSWORD":                 "vc_pass",
		"VIBECHECK_DB_NAME":                     "vibecheck_test",
		"VIBECHECK_DB_IS_SSL":                   "false",
		"VIBECHECK_STORE_INPUTS":                "true",
		"VIBECHECK_DATA_KEY":                    "data-key-value",
		"VIBECHECK_EVALUATION_RETENTION":        "48h",
		"VIBECHECK_IDEMPOTENCY_RETENTION":       "12h",
		"VIBECHECK_RETENTION_CLEANUP_INTERVAL":  "30m",
	}

	for key, val := range overrides {
		t.Setenv(key, val)
	}

	got, err := config.Parse()
	require.NoError(t, err)

	want := config.Config{
		HTTPListenAddress:    "0.0.0.0:9000",
		MetricsListenAddress: "0.0.0.0:9001",
		APIToken:             "test-token",

		PolicyDir:           "/tmp/policies",
		AllowInlinePolicies: true,

		MaxRequestBytes: 2097152,
		MaxStateBytes:   1048576,
		MaxFacts:        256,
		MaxQuestions:    32,
		MaxPolicyBytes:  524288,

		MaxMetadataEntries:     32,
		MaxMetadataValueLength: 512,

		RequestsPerMinute: 1200,
		RateLimitBurst:    120,

		TypeSafeBaseURL:          "https://ts.example.test",
		TypeSafeAPIKey:           "typesafe-key",
		TypeSafeModel:            "jev-test",
		TypeSafeTimeout:          20 * time.Second,
		TypeSafeMaxResponseBytes: 2097152,
		TypeSafeConcurrency:      64,
		TypeSafeMaxAttempts:      5,

		DBDriver:            "postgres",
		DBSQLitePath:        "/tmp/test.sqlite",
		DBSQLiteBusyTimeout: 2 * time.Second,
		DBHostname:          "db.internal",
		DBPort:              6543,
		DBUsername:          "vc_user",
		DBPassword:          "vc_pass",
		DBName:              "vibecheck_test",
		DBIsSSL:             false,

		StoreInputs:          true,
		DataKey:              "data-key-value",
		EvaluationRetention:  48 * time.Hour,
		IdempotencyRetention: 12 * time.Hour,

		RetentionCleanupEvery: 30 * time.Minute,
	}

	assert.Equal(t, want, got)
}

func TestValidateRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr error
	}{
		{
			name: "empty http listen address",
			mutate: func(c *config.Config) {
				c.HTTPListenAddress = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "empty metrics listen address",
			mutate: func(c *config.Config) {
				c.MetricsListenAddress = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "metrics listener matches http listener",
			mutate: func(c *config.Config) {
				c.MetricsListenAddress = c.HTTPListenAddress
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max request bytes not positive",
			mutate: func(c *config.Config) {
				c.MaxRequestBytes = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max state bytes not positive",
			mutate: func(c *config.Config) {
				c.MaxStateBytes = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max facts not positive",
			mutate: func(c *config.Config) {
				c.MaxFacts = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max questions not positive",
			mutate: func(c *config.Config) {
				c.MaxQuestions = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max questions exceeds compiler ceiling",
			mutate: func(c *config.Config) {
				c.MaxQuestions = config.DefaultMaxQuestions + 1
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max policy bytes not positive",
			mutate: func(c *config.Config) {
				c.MaxPolicyBytes = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max metadata entries not positive",
			mutate: func(c *config.Config) {
				c.MaxMetadataEntries = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max metadata value length not positive",
			mutate: func(c *config.Config) {
				c.MaxMetadataValueLength = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "typesafe max response bytes not positive",
			mutate: func(c *config.Config) {
				c.TypeSafeMaxResponseBytes = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "typesafe concurrency not positive",
			mutate: func(c *config.Config) {
				c.TypeSafeConcurrency = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "typesafe max attempts not positive",
			mutate: func(c *config.Config) {
				c.TypeSafeMaxAttempts = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "requests per minute not positive",
			mutate: func(c *config.Config) {
				c.RequestsPerMinute = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "rate limit burst not positive",
			mutate: func(c *config.Config) {
				c.RateLimitBurst = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "max state bytes exceeds max request bytes",
			mutate: func(c *config.Config) {
				c.MaxStateBytes = c.MaxRequestBytes + 1
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "typesafe timeout not positive",
			mutate: func(c *config.Config) {
				c.TypeSafeTimeout = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "unknown db driver",
			mutate: func(c *config.Config) {
				c.DBDriver = "mysql"
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "sqlite driver missing sqlite path",
			mutate: func(c *config.Config) {
				c.DBSQLitePath = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "postgres driver missing hostname",
			mutate: func(c *config.Config) {
				*c = validPostgresConfig()
				c.DBHostname = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "postgres driver missing username",
			mutate: func(c *config.Config) {
				*c = validPostgresConfig()
				c.DBUsername = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "postgres driver missing password",
			mutate: func(c *config.Config) {
				*c = validPostgresConfig()
				c.DBPassword = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "postgres driver missing database name",
			mutate: func(c *config.Config) {
				*c = validPostgresConfig()
				c.DBName = ""
			},
			wantErr: commerr.ErrRequiredConfigValueNotSet,
		},
		{
			name: "db port not positive",
			mutate: func(c *config.Config) {
				*c = validPostgresConfig()
				c.DBPort = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "evaluation retention negative",
			mutate: func(c *config.Config) {
				c.EvaluationRetention = -time.Second
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "idempotency retention negative",
			mutate: func(c *config.Config) {
				c.IdempotencyRetention = -time.Second
			},
			wantErr: commerr.ErrInvalidValue,
		},
		{
			name: "retention cleanup interval not positive",
			mutate: func(c *config.Config) {
				c.RetentionCleanupEvery = 0
			},
			wantErr: commerr.ErrInvalidValue,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := validBaseConfig()
			tc.mutate(&cfg)

			err := cfg.Validate()
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestSecurityInvariantStoreInputsRequiresDataKey is the one rule that
// matters most in this package: storing raw input in the clear must never
// be reachable by flipping VIBECHECK_STORE_INPUTS alone.
func TestSecurityInvariantStoreInputsRequiresDataKey(t *testing.T) {
	t.Parallel()

	t.Run("store inputs without a data key is refused", func(t *testing.T) {
		t.Parallel()

		cfg := validBaseConfig()
		cfg.StoreInputs = true
		cfg.DataKey = ""

		err := cfg.Validate()
		require.ErrorIs(t, err, commerr.ErrRequiredConfigValueNotSet)
	})

	t.Run("store inputs with a data key is accepted", func(t *testing.T) {
		t.Parallel()

		cfg := validBaseConfig()
		cfg.StoreInputs = true
		cfg.DataKey = "a-data-key"

		require.NoError(t, cfg.Validate())
	})
}

func TestAuthEnabled(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		token string
		want  bool
	}{
		{name: "empty token", token: "", want: false},
		{name: "non-empty token", token: "some-token", want: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := validBaseConfig()
			cfg.APIToken = tc.token

			assert.Equal(t, tc.want, cfg.AuthEnabled())
		})
	}
}

func TestDBConfigMapsFields(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		cfg  config.Config
		want db.Config
	}{
		{
			name: "sqlite",
			cfg:  validBaseConfig(),
			want: db.Config{
				Driver:            db.DriverSQLite,
				SQLitePath:        "/data/vibecheck.sqlite",
				SQLiteBusyTimeout: 5 * time.Second,
				Postgres: db.PostgresConfig{
					Hostname: "localhost",
					Port:     5432,
					Username: "",
					Password: "",
					Database: "vibecheck",
					IsSSL:    true,
				},
			},
		},
		{
			name: "postgres",
			cfg:  validPostgresConfig(),
			want: db.Config{
				Driver:            db.DriverPostgres,
				SQLitePath:        "/data/vibecheck.sqlite",
				SQLiteBusyTimeout: 5 * time.Second,
				Postgres: db.PostgresConfig{
					Hostname: "db.internal",
					Port:     6543,
					Username: "vc_user",
					Password: "vc_pass",
					Database: "vibecheck_test",
					IsSSL:    true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.cfg.DBConfig())
		})
	}
}
