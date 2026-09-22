package server

import (
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
)

// Paths the router owns outside the generated contract.
const (
	// APIBasePath is the URL major. It matches servers.url in api/api.yml.
	APIBasePath = "/v1"

	// HealthPath is public and unauthenticated: a liveness probe that
	// needs a credential cannot tell you the process is alive.
	HealthPath = "/healthz"

	// ReadyPath reports coarse dependency readiness only.
	ReadyPath = "/ready"

	// MCPPath is the Streamable HTTP endpoint.
	//
	// Both the bare path and the trailing-slash form are registered as
	// exact patterns. Registering only "/mcp/" would make net/http answer a
	// POST to "/mcp" with a 301 to "/mcp/", and a redirected POST loses its
	// body. The "{$}" suffix keeps the second pattern an exact match rather
	// than a subtree, so neither form ever redirects.
	MCPPath      = "/mcp"
	MCPSlashPath = "/mcp/{$}"
)

// healthKeyStatus is the single field in the liveness and readiness bodies.
// Probes parse it, so it is a contract rather than a message.
const healthKeyStatus = "status"

// Route templates used as bounded metric labels. They are the OpenAPI path
// templates, never the raw request path.
const (
	routeEvaluations       = "/v1/evaluations"
	routeEvaluationByID    = "/v1/evaluations/{evaluationId}"
	routeEvaluationFeedbck = "/v1/evaluations/{evaluationId}/feedback"
	routePolicies          = "/v1/policies"
	routePolicyByRef       = "/v1/policies/{policyName}/versions/" +
		"{policyVersion}"
	routePolicyValidate = "/v1/policies/validate"
	routeHealth         = HealthPath
	routeReady          = ReadyPath
	routeMCP            = MCPPath
)

// RouterConfig is everything the router needs that is not a service.
type RouterConfig struct {
	// APIToken enables bearer auth when non-empty.
	APIToken string

	// MaxRequestBytes bounds every request body.
	MaxRequestBytes int64

	// RequestsPerMinute and RateLimitBurst bound the public surface.
	RequestsPerMinute int
	RateLimitBurst    int

	// MCPHandler is mounted at the exact MCP paths. Nil leaves MCP off.
	MCPHandler http.Handler

	// Metrics records request counts and durations.
	Metrics *metrics.Metrics

	// Ready reports whether dependencies are usable. It must be cheap: a
	// readiness probe runs often and must never touch the provider.
	Ready func() bool
}

// Router assembles the public listener's handler.
type Router struct {
	config RouterConfig
	server *Server

	// shuttingDown flips before the drain begins so readiness fails fast
	// and a load balancer stops sending new work.
	shuttingDown atomic.Bool
}

// NewRouter builds the router.
func NewRouter(server *Server, config RouterConfig) *Router {
	return &Router{config: config, server: server}
}

// BeginShutdown makes readiness report false without closing the listener, so
// in-flight requests finish while new ones are steered away.
func (r *Router) BeginShutdown() {
	r.shuttingDown.Store(true)
}

// Handler returns the fully wrapped public handler.
//
// Routes are registered on a plain http.ServeMux rather than through a
// framework router because the MCP endpoint needs exact-path control that a
// subtree-matching router does not give.
func (r *Router) Handler() http.Handler {
	mux := http.NewServeMux()

	// The plain NewStrictHandler answers a decode failure with
	// http.Error, which is text/plain and not the project envelope, and
	// answers a response failure by echoing err.Error() into the body.
	// Both hooks are replaced so every reply on this listener is the same
	// JSON shape and no internal error text reaches a caller.
	strict := api.NewStrictHandlerWithOptions(
		r.server, nil,
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  r.writeRequestError,
			ResponseErrorHandlerFunc: r.writeResponseError,
		},
	)

	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseURL:    APIBasePath,
		BaseRouter: mux,
		Middlewares: []api.MiddlewareFunc{
			api.MiddlewareFunc(r.apiMiddleware()),
		},
		ErrorHandlerFunc: r.writeRequestError,
	})

	mux.Handle(HealthPath, r.observability(routeHealth, r.healthHandler()))
	mux.Handle(ReadyPath, r.observability(routeReady, r.readyHandler()))

	r.mountMCP(mux)

	return mux
}

// mountMCP registers the MCP handler at both exact paths.
func (r *Router) mountMCP(mux *http.ServeMux) {
	if r.config.MCPHandler == nil {
		return
	}

	handler := chain(
		r.config.MCPHandler,
		RequestID(),
		Recovery(),
		SecurityHeaders(),
		AccessLog(routeMCP),
		Metrics(r.config.Metrics, routeMCP),
		RateLimit(NewRateLimiter(
			r.config.RequestsPerMinute, r.config.RateLimitBurst,
		)),
		BearerAuth(r.config.APIToken),
		BodyLimit(r.config.MaxRequestBytes),
	)

	mux.Handle(MCPPath, handler)
	mux.Handle(MCPSlashPath, handler)
}

// apiMiddleware is the chain every generated route runs through. The
// generated router applies it per operation, which is why the route label is
// resolved from the request pattern rather than baked in here.
func (r *Router) apiMiddleware() Middleware {
	limiter := NewRateLimiter(
		r.config.RequestsPerMinute, r.config.RateLimitBurst,
	)

	return func(next http.Handler) http.Handler {
		// Everything that can reject a request sits inside the logger, so a
		// rejected request still produces one completion record.
		guarded := chain(
			next,
			RateLimit(limiter),
			BearerAuth(r.config.APIToken),
			BodyLimit(r.config.MaxRequestBytes),
			r.strictJSONBodies(),
		)

		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// The route template is only known once the mux has matched, so
			// the logging and metrics wrappers are built per request.
			route := routeTemplate(req)

			// RequestID is outermost: it seeds the correlation ID before
			// anything logs, so every record for this request carries it.
			// Wrapping the logger outside RequestID instead would drop
			// request_id from the access log of every API route.
			chain(
				guarded,
				RequestID(),
				Recovery(),
				SecurityHeaders(),
				AccessLog(route),
				Metrics(r.config.Metrics, route),
			).ServeHTTP(w, req)
		})
	}
}

// observability is the reduced chain for the probe endpoints. They are
// deliberately unauthenticated and unlimited: a probe that can be rate
// limited reports the service down under load.
func (r *Router) observability(route string, next http.Handler) http.Handler {
	return chain(
		next,
		RequestID(),
		Recovery(),
		SecurityHeaders(),
		AccessLog(route),
		Metrics(r.config.Metrics, route),
	)
}

func (r *Router) healthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		aichteeteapee.WriteJSON(
			w, http.StatusOK, map[string]string{healthKeyStatus: "ok"},
		)
	})
}

func (r *Router) readyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if r.shuttingDown.Load() || !r.config.Ready() {
			aichteeteapee.WriteJSON(
				w, http.StatusServiceUnavailable,
				map[string]string{healthKeyStatus: "not_ready"},
			)

			return
		}

		aichteeteapee.WriteJSON(
			w, http.StatusOK, map[string]string{healthKeyStatus: "ready"},
		)
	})
}

// writeRequestError answers a request the generated binder refused: a
// malformed path parameter, an unparseable query value, or a body that is not
// JSON. It keeps the same envelope as every other failure.
func (r *Router) writeRequestError(
	w http.ResponseWriter,
	r0 *http.Request,
	err error,
) {
	ctxscope.GetLogger(r0.Context()).Debug(
		"request rejected at the binder", "err", err,
	)

	// A body that tripped the size limit is 413, which is what the contract
	// declares for these routes. Reporting it as a generic 400 tells the
	// caller the request was malformed when it was simply too big, and they
	// would retry the same bytes.
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		aichteeteapee.WriteJSON(
			w,
			http.StatusRequestEntityTooLarge,
			aichteeteapee.ErrorResponse{
				Code: aichteeteapee.ErrorCodeFromHTTPStatus(
					http.StatusRequestEntityTooLarge,
				),
				Message: "the request body is too large",
			})

		return
	}

	aichteeteapee.WriteJSON(
		w,
		http.StatusBadRequest,
		aichteeteapee.ErrorResponse{
			Code:    aichteeteapee.ErrorCodeBadRequest,
			Message: "the request could not be decoded",
		})
}

// writeResponseError answers a failure that happened after the handler
// returned, such as encoding a response.
//
// The cause is logged and deliberately not echoed. A response-side error can
// carry internal detail, and a caller can do nothing with it.
func (r *Router) writeResponseError(
	w http.ResponseWriter,
	r0 *http.Request,
	err error,
) {
	ctxscope.GetLogger(r0.Context()).Error(
		"writing the response failed", "err", err,
	)

	aichteeteapee.WriteJSON(
		w,
		http.StatusInternalServerError,
		aichteeteapee.ErrorResponseInternalServerError,
	)
}

// routeTemplate reports the matched pattern as a bounded metric label.
//
// http.Request.Pattern carries the registered pattern since Go 1.23, which is
// exactly the template the OpenAPI contract declares. Falling back to a fixed
// string keeps an unmatched request from minting a series off its raw path.
func routeTemplate(r *http.Request) string {
	if r.Pattern == "" {
		return "unmatched"
	}

	return r.Pattern
}
