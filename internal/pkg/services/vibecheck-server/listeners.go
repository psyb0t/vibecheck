package vibecheckserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
)

// promHandler serves the registry this process owns, not the default one.
func promHandler(recorder *metrics.Metrics) http.Handler {
	return promhttp.HandlerFor(
		recorder.Registry(),
		promhttp.HandlerOpts{
			// An error while collecting must not take the scrape endpoint
			// down; a partial scrape is more useful than a 500.
			ErrorHandling: promhttp.ContinueOnError,
		},
	)
}

// newHTTPServer applies the hardening floor to one listener.
func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
}

// serve runs one listener until it stops, reporting the outcome on errs.
//
// A closed server is the expected end of a graceful shutdown, so
// http.ErrServerClosed is reported as a nil error rather than a failure.
func serve(
	ctx context.Context,
	label string,
	server *http.Server,
	errs chan<- error,
) {
	listenerCtx := ctxscope.Set(ctx, ctxscope.Attr(scopeKeyListener, label))
	ctxscope.GetLogger(listenerCtx).Info(
		"listener started", "address", server.Addr,
	)

	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		errs <- nil

		return
	}

	errs <- ctxerrors.Wrapf(err, "%s listener failed", label)
}

// shutdown drains one listener within the grace period.
func shutdown(ctx context.Context, label string, server *http.Server) error {
	drainCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), shutdownTimeout,
	)
	defer cancel()

	if err := server.Shutdown(drainCtx); err != nil {
		return ctxerrors.Wrapf(err, "drain %s listener", label)
	}

	return nil
}

// drainGrace gives load balancers a moment to observe the failing readiness
// probe before in-flight work is cut off.
//
// Without it, a rolling deploy races: the process stops accepting while a
// balancer is still routing to it, and those requests fail for no reason.
func drainGrace(ctx context.Context, grace time.Duration) {
	if grace <= 0 {
		return
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
