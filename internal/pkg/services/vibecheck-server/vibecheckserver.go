// Package vibecheckserver is Vibecheck's runnable service.
//
// It owns startup order and shutdown order and nothing else. Every decision
// about policies, providers, persistence, or transport lives in the packages
// it wires together; this package only makes sure they come up in an order
// where each one's dependencies already exist, and go down in the reverse.
package vibecheckserver

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/config"
)

// VibecheckServer serves the REST and MCP surfaces over one policy set, one
// database, and one decision provider.
type VibecheckServer struct {
	config config.Config

	// stopped closes once, when shutdown is requested. Run selects on it,
	// so Stop can trigger the drain without owning any of the listeners.
	stopped  chan struct{}
	stopOnce sync.Once
}

// New parses and validates the deployment configuration.
//
// Configuration problems surface here, at registration time, rather than on
// the first request that happens to read a bad value.
func New() (*VibecheckServer, error) {
	cfg, err := config.Parse()
	if err != nil {
		return nil, ctxerrors.Wrap(err, "parse vibecheck-server config")
	}

	return &VibecheckServer{
		config:  cfg,
		stopped: make(chan struct{}),
	}, nil
}

func (s *VibecheckServer) Name() string {
	return ServiceName
}

// Run brings the service up and blocks until it is asked to stop or a
// listener fails.
//
// The public listener and the internal metrics listener run together. Either
// one failing ends the service: a process that serves traffic it cannot
// measure, or measures traffic it cannot serve, is not a state worth staying
// in.
func (s *VibecheckServer) Run(ctx context.Context) error {
	ctx = ctxscope.Set(ctx, ctxscope.Attr(scopeKeyService, ServiceName))
	logger := ctxscope.GetLogger(ctx)
	logger.Info("starting service")

	deps, err := s.build(ctx)
	if err != nil {
		return err
	}
	defer deps.close(ctx)

	retentionCtx, stopRetention := context.WithCancel(ctx)
	defer stopRetention()

	var retentionDone sync.WaitGroup

	retentionDone.Go(func() {
		runRetention(retentionCtx, deps.repo, deps.metrics, s.retention())
	})

	public := newHTTPServer(
		s.config.HTTPListenAddress, deps.router.Handler(),
	)
	internal := newHTTPServer(
		s.config.MetricsListenAddress, metricsHandler(deps.metrics),
	)

	errs := make(chan error, 2) //nolint:mnd // one slot per listener

	go serve(ctx, listenerPublic, public, errs)
	go serve(ctx, listenerMetrics, internal, errs)

	logger.Info("service ready")

	runErr := s.waitForStop(ctx, errs)

	stopRetention()
	retentionDone.Wait()

	return errors.Join(
		runErr,
		s.drain(ctx, deps, public, internal),
	)
}

// waitForStop blocks until shutdown is requested or a listener fails.
func (s *VibecheckServer) waitForStop(
	ctx context.Context,
	errs <-chan error,
) error {
	logger := ctxscope.GetLogger(ctx)

	select {
	case <-ctx.Done():
		logger.Info("context cancelled, stopping service")

		return nil
	case <-s.stopped:
		logger.Info("stop requested, stopping service")

		return nil
	case err := <-errs:
		if err != nil {
			logger.Error("listener failed", "err", err)
		}

		return err
	}
}

// drain fails readiness, waits out the configured grace, then closes both
// listeners.
func (s *VibecheckServer) drain(
	ctx context.Context,
	deps *dependencies,
	public, internal *http.Server,
) error {
	deps.router.BeginShutdown()
	drainGrace(ctx, config.DefaultShutdownDrainGrace)

	return errors.Join(
		shutdown(ctx, listenerPublic, public),
		shutdown(ctx, listenerMetrics, internal),
	)
}

// Stop asks Run to begin a graceful shutdown. It is safe to call more than
// once and never blocks on the drain itself.
func (s *VibecheckServer) Stop(ctx context.Context) error {
	serviceCtx := ctxscope.Set(ctx, ctxscope.Attr(scopeKeyService, ServiceName))
	ctxscope.GetLogger(serviceCtx).Info("stopping service")

	s.stopOnce.Do(func() { close(s.stopped) })

	return nil
}

// retention projects the configured windows onto the cleanup loop's shape.
func (s *VibecheckServer) retention() retentionSettings {
	return retentionSettings{
		every:       s.config.RetentionCleanupEvery,
		evaluations: s.config.EvaluationRetention,
	}
}
