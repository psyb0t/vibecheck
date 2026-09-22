package server

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
	"golang.org/x/time/rate"
)

// Middleware is the standard net/http wrapper shape. The chain is applied
// outermost first.
type Middleware func(http.Handler) http.Handler

// Request-ID handling. A caller-supplied ID is echoed only when it looks like
// an ID; anything else is replaced, so a caller cannot inject arbitrary text
// into every log line for the request.
const (
	maxRequestIDLength = 128
	scopeKeyRequestID  = "request_id"
	scopeKeyRoute      = "route"
)

// secondsPerMinute converts the configured per-minute rate into the
// per-second rate the limiter takes.
const secondsPerMinute = 60

// Stable machine-readable values for the `reason` field on a rejection. An
// operator greps these; they are not prose and must not be reworded.
const (
	reasonHandlerPanic      = "handler_panic"
	reasonAuthSchemeMissing = "auth_scheme_missing"
	// #nosec G101 -- A log reason, not a credential. It records that a
	// comparison failed and never carries the value that was compared.
	reasonAuthTokenMismatch = "auth_token_mismatch"
	reasonRateLimited       = "rate_limited"
)

// chain applies middlewares so the first entry is the outermost wrapper.
func chain(handler http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	return handler
}

// statusRecorder captures the status code so the logging and metrics
// middleware can report it. net/http gives no way to read it back.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	written, err := r.ResponseWriter.Write(body)
	if err != nil {
		//nolint:wrapcheck // net/http inspects this error itself, so it has
		// to travel back unchanged.
		return written, err
	}

	return written, nil
}

// Flush keeps streaming transports working through the wrapper. The MCP
// Streamable HTTP handler writes SSE, which needs this.
func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *statusRecorder) statusOrOK() int {
	if r.status == 0 {
		return http.StatusOK
	}

	return r.status
}

// RequestID seeds the correlation ID before anything else runs, so a request
// rejected by auth or by the body limit is still traceable.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := sanitiseRequestID(r.Header.Get(api.HeaderRequestID))

			w.Header().Set(api.HeaderRequestID, requestID)

			ctx := ctxscope.Set(
				r.Context(),
				ctxscope.Attr(scopeKeyRequestID, requestID),
			)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sanitiseRequestID keeps a caller-supplied UUID and mints one otherwise.
func sanitiseRequestID(candidate string) string {
	if candidate == "" || len(candidate) > maxRequestIDLength {
		return uuid.NewString()
	}

	if _, err := uuid.Parse(candidate); err != nil {
		return uuid.NewString()
	}

	return candidate
}

// AccessLog records one completed request. It never logs the body, the query
// string, or arbitrary headers.
func AccessLog(route string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ctxscope.Set(
				r.Context(), ctxscope.Attr(scopeKeyRoute, route),
			)
			r = r.WithContext(ctx)

			logger := ctxscope.GetLogger(ctx)
			logger.Debug("request started", "method", r.Method, "route", route)

			recorder := &statusRecorder{ResponseWriter: w}
			startedAt := time.Now()

			next.ServeHTTP(recorder, r)

			logger.Info(
				"request completed",
				"method", r.Method,
				"route", route,
				"status", recorder.statusOrOK(),
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)
		})
	}
}

// Metrics records request count, duration, and in-flight depth against the
// route template rather than the raw path.
func Metrics(recorderSet *metrics.Metrics, route string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorderSet.HTTPInFlight().Inc()
			defer recorderSet.HTTPInFlight().Dec()

			recorder := &statusRecorder{ResponseWriter: w}
			startedAt := time.Now()

			next.ServeHTTP(recorder, r)

			recorderSet.ObserveHTTP(
				r.Method, route,
				recorder.statusOrOK(),
				time.Since(startedAt).Seconds(),
			)
		})
	}
}

// Recovery turns a handler panic into the standard 500 envelope instead of a
// dropped connection.
func Recovery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Captured before the handler runs. A handler is free to swap
			// the request out from under us, and the recovery path still
			// needs the scope this request arrived with.
			ctx := r.Context()

			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				ctxscope.GetLogger(ctx).Error(
					"handler panicked", "panic", recovered,
				)

				writeError(
					ctx, w, http.StatusInternalServerError,
					aichteeteapee.ErrorResponseInternalServerError,
					reasonHandlerPanic,
				)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets the response headers that cost nothing and remove a
// class of browser-side problems. There is no HTML surface here, so the CSP
// simply forbids everything.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()
			header.Set("X-Content-Type-Options", "nosniff")
			header.Set("X-Frame-Options", "DENY")
			header.Set("Referrer-Policy", "no-referrer")
			header.Set("Content-Security-Policy", "default-src 'none'")
			header.Set("Cache-Control", "no-store")

			next.ServeHTTP(w, r)
		})
	}
}

// BearerAuth enforces the optional API token.
//
// An empty configured token disables the check entirely, which is the
// documented trusted-loopback mode. Comparison is constant time so a wrong
// token cannot be recovered by timing the response.
func BearerAuth(token string) Middleware {
	const scheme = "Bearer "

	expected := []byte(token)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				next.ServeHTTP(w, r)

				return
			}

			header := r.Header.Get(aichteeteapee.HeaderNameAuthorization)
			if !strings.HasPrefix(header, scheme) {
				writeError(
					r.Context(), w, http.StatusUnauthorized,
					aichteeteapee.ErrorResponseUnauthorized,
					reasonAuthSchemeMissing,
				)

				return
			}

			supplied := []byte(strings.TrimPrefix(header, scheme))
			if subtle.ConstantTimeCompare(supplied, expected) != 1 {
				writeError(
					r.Context(), w, http.StatusUnauthorized,
					aichteeteapee.ErrorResponseUnauthorized,
					reasonAuthTokenMismatch,
				)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimit caps the request body before anything decodes it.
//
// http.MaxBytesReader makes the decoder fail rather than buffering an
// unbounded body, so an oversized request costs the configured ceiling and
// not one byte more.
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimiter is a fixed-capacity token bucket shared by every caller.
//
// The limit is global rather than per-client on purpose: Vibecheck sits
// behind a reverse proxy or on loopback, so the remote address is usually the
// proxy and a per-address bucket would either be one bucket anyway or be
// trivially evaded by a spoofed forwarding header.
type RateLimiter struct {
	limiter *rate.Limiter
}

// NewRateLimiter builds a limiter from a per-minute rate and a burst.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	perSecond := float64(perMinute) / secondsPerMinute

	return &RateLimiter{
		limiter: rate.NewLimiter(rate.Limit(perSecond), burst),
	}
}

// Allow reports whether one more request fits in the budget. It is safe for
// concurrent use.
func (l *RateLimiter) Allow() bool {
	return l.limiter.Allow()
}

// RateLimit rejects a request that exceeds the configured budget.
func RateLimit(limiter *RateLimiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				writeError(
					r.Context(), w, http.StatusTooManyRequests,
					aichteeteapee.ErrorResponseTooManyRequests,
					reasonRateLimited,
				)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeError emits the project error envelope and records why the request
// never reached a handler. Handlers return typed responses instead; this is
// only for the middleware layer, which runs before the generated handler
// exists and is therefore the only place a rejection would otherwise be
// invisible.
func writeError(
	ctx context.Context,
	w http.ResponseWriter,
	status int,
	body aichteeteapee.ErrorResponse,
	reason string,
) {
	ctxscope.GetLogger(ctx).Warn(
		"request rejected",
		"status", status,
		"code", body.Code,
		"reason", reason,
	)

	aichteeteapee.WriteJSON(w, status, body)
}
