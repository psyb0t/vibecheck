package vibecheckserver

import (
	"context"
	"net/http"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/config"
	"github.com/psyb0t/vibecheck/internal/pkg/core/evaluations"
	"github.com/psyb0t/vibecheck/internal/pkg/core/policies"
	"github.com/psyb0t/vibecheck/internal/pkg/db"
	"github.com/psyb0t/vibecheck/internal/pkg/db/repositories"
	httpserver "github.com/psyb0t/vibecheck/internal/pkg/http/server"
	"github.com/psyb0t/vibecheck/internal/pkg/mcp"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	"github.com/psyb0t/vibecheck/internal/pkg/provider/jev"
	"github.com/psyb0t/vibecheck/internal/pkg/secrets"
)

// dependencies is everything the running service owns. It exists so Run has
// one value to hand around and one place to close.
type dependencies struct {
	database *db.Database
	repo     *repositories.Query
	router   *httpserver.Router
	metrics  *metrics.Metrics
}

// build opens every dependency in the order they depend on each other and
// returns them wired together.
//
// Anything that can be wrong with the deployment is discovered here, before
// a listener accepts a single connection: an unreadable policy directory, an
// unreachable database, a failed migration, a malformed data key.
func (s *VibecheckServer) build(ctx context.Context) (*dependencies, error) {
	logger := ctxscope.GetLogger(ctx)

	recorder := metrics.New()
	recorder.SetBuildInfo(buildIdentity())

	policySet, err := policy.LoadDirWithMaxQuestions(
		s.config.PolicyDir,
		s.config.MaxPolicyBytes,
		s.config.MaxQuestions,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "load policies")
	}

	logger.Info("policies loaded", "count", policySet.Len())

	database, err := db.Open(
		ctx,
		s.config.DBConfig(),
		db.WithObserver(recorder),
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "open database")
	}

	logger.Info("database ready", "driver", database.Driver.String())

	repo := repositories.Use(database.Gorm)

	router, err := s.buildRouterStack(policySet, database, repo, recorder)
	if err != nil {
		return nil, closeAfter(database, err)
	}

	return &dependencies{
		database: database,
		repo:     repo,
		router:   router,
		metrics:  recorder,
	}, nil
}

// buildRouterStack assembles the core services and the public handler.
//
// It is separate from build so the caller keeps one place that owns closing
// the database when a later step fails.
func (s *VibecheckServer) buildRouterStack(
	policySet *policy.Set,
	database *db.Database,
	repo *repositories.Query,
	recorder *metrics.Metrics,
) (*httpserver.Router, error) {
	sealer, err := buildSealer(s.config)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build sealer")
	}

	decisionProvider, err := buildProvider(s.config, recorder)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build provider")
	}

	policyService := policies.NewWithMaxQuestions(
		policySet,
		s.config.MaxPolicyBytes,
		s.config.MaxQuestions,
	)
	evaluationService := evaluations.New(
		policyService,
		decisionProvider,
		repo,
		sealer,
		evaluationConfig(s.config),
		evaluations.WithObserver(recorder),
	)

	return buildRouter(s.config, recorder, database, httpserver.New(
		evaluationService, policyService,
	), mcp.Services{
		Evaluations: evaluationService,
		Policies:    policyService,
	})
}

// close releases what build opened. It is safe on a partially built value.
func (d *dependencies) close(ctx context.Context) {
	if d == nil || d.database == nil {
		return
	}

	if err := d.database.Close(); err != nil {
		ctxscope.GetLogger(ctx).Error("close database failed", "err", err)
	}
}

// closeAfter releases the database when a later build step failed, so a
// half-built service does not leak the connection it already opened.
func closeAfter(database *db.Database, cause error) error {
	if err := database.Close(); err != nil {
		return ctxerrors.Wrap(cause, "close database after build failure")
	}

	return cause
}

// buildSealer returns the input sealer, or nil when retention is off.
//
// A nil sealer is the documented "do not retain input" mode. Config
// validation already refuses StoreInputs without a key, so reaching the key
// path here means a key was supplied deliberately.
func buildSealer(cfg config.Config) (*secrets.Sealer, error) {
	if !cfg.StoreInputs {
		return nil, nil //nolint:nilnil // nil sealer is the off switch
	}

	sealer, err := secrets.NewSealer(cfg.DataKey)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build input sealer")
	}

	return sealer, nil
}

// buildProvider builds the Jev adapter with metrics wired to every attempt.
func buildProvider(
	cfg config.Config,
	recorder *metrics.Metrics,
) (provider.Provider, error) {
	client, err := jev.New(
		jev.Config{
			BaseURL:          cfg.TypeSafeBaseURL,
			APIKey:           cfg.TypeSafeAPIKey,
			DefaultModel:     cfg.TypeSafeModel,
			Timeout:          cfg.TypeSafeTimeout,
			MaxResponseBytes: int64(cfg.TypeSafeMaxResponseBytes),
			Concurrency:      cfg.TypeSafeConcurrency,
			MaxAttempts:      cfg.TypeSafeMaxAttempts,
		},
		jev.WithAttemptObserver(func(attempt provider.Attempt) {
			recorder.ObserveProviderAttempt(
				attempt.StatusCode,
				attempt.Err != nil,
				attempt.Duration.Seconds(),
			)
		}),
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build jev client")
	}

	return client, nil
}

// evaluationConfig projects deployment config onto the pipeline's own shape.
func evaluationConfig(cfg config.Config) evaluations.Config {
	return evaluations.Config{
		AllowInlinePolicies:    cfg.AllowInlinePolicies,
		MaxPolicyBytes:         cfg.MaxPolicyBytes,
		MaxStateBytes:          cfg.MaxStateBytes,
		MaxQuestions:           cfg.MaxQuestions,
		MaxFacts:               cfg.MaxFacts,
		MaxMetadataEntries:     cfg.MaxMetadataEntries,
		MaxMetadataValueLength: cfg.MaxMetadataValueLength,
		StoreInputs:            cfg.StoreInputs,
		IdempotencyRetention:   cfg.IdempotencyRetention,
	}
}

// buildRouter assembles the public handler with REST and MCP on one mux.
func buildRouter(
	cfg config.Config,
	recorder *metrics.Metrics,
	database *db.Database,
	restServer *httpserver.Server,
	mcpServices mcp.Services,
) (*httpserver.Router, error) {
	maxRequestBytes := int64(cfg.MaxRequestBytes)

	mcpHandler, err := mcp.Handler(
		mcpServices, recorder,
		mcp.HandlerConfig{MaxRequestBytes: maxRequestBytes},
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build the MCP handler")
	}

	return httpserver.NewRouter(restServer, httpserver.RouterConfig{
		APIToken:          cfg.APIToken,
		MaxRequestBytes:   maxRequestBytes,
		RequestsPerMinute: cfg.RequestsPerMinute,
		RateLimitBurst:    cfg.RateLimitBurst,
		Metrics:           recorder,
		MCPHandler:        mcpHandler,
		Ready:             readinessProbe(database),
	}), nil
}

// readinessProbe reports whether the database still answers.
//
// It is deliberately the only dependency checked. A readiness probe that
// calls the decision provider would bill a request per probe and would mark
// the service down for a provider blip it is designed to retry through.
func readinessProbe(database *db.Database) func() bool {
	return func() bool {
		return database.SQL.Ping() == nil
	}
}

// buildIdentity reads the version and commit the linker stamped into the
// process scope, so the build_info metric matches the running binary.
func buildIdentity() (string, string) {
	scope := ctxscope.GetGlobal()

	version, _ := scope[scopeKeyVersion].(string)
	commit, _ := scope[scopeKeyCommit].(string)

	return version, commit
}

// metricsHandler serves the Prometheus registry on the internal listener.
func metricsHandler(recorder *metrics.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(MetricsPath, promHandler(recorder))

	return mux
}
