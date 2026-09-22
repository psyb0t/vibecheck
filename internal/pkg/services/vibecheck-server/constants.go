package vibecheckserver

import "time"

// ServiceName is the name the service manager registers and the value that
// goes into every log record this service emits.
const ServiceName = "vibecheck-server"

// Logging scope keys. They are the stable field names an operator greps for.
const (
	scopeKeyService  = "service"
	scopeKeyListener = "listener"
)

// Process-identity scope keys. cmd/main.go seeds these globals from the
// linker stamps, and the build_info metric reads them back so the metric and
// the logs describe the same binary.
const (
	scopeKeyVersion = "version"
	scopeKeyCommit  = "commit"
)

// Listener labels, used in logs so two listeners on one process stay
// distinguishable.
const (
	listenerPublic  = "public"
	listenerMetrics = "metrics"
)

// MetricsPath is where the internal listener serves Prometheus text. It is
// deliberately not on the public listener: scrape data exposes traffic shape
// and error rates that a public caller has no business reading.
const MetricsPath = "/metrics"

// HTTP server hardening. These bound how long a single connection can hold
// resources, which is what stops a slow reader from parking a goroutine
// forever.
//
// writeTimeout stays off. Go measures it from the start of the request, and
// an evaluation legitimately spends the provider timeout plus a bounded
// retry series before it can write anything. A fixed deadline here would cut
// off correct answers under load. The response is still bounded, by the
// request context, the provider's own per-attempt timeout, and its
// whole-series elapsed ceiling.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 0
	idleTimeout       = 120 * time.Second
	maxHeaderBytes    = 1 << 20
)

// shutdownTimeout bounds the graceful drain. Past it, remaining connections
// are cut rather than holding the process open indefinitely.
const shutdownTimeout = 20 * time.Second

// retentionBatchSize bounds one cleanup delete. A retention pass over a large
// backlog runs as many bounded batches instead of one statement that would
// hold write locks long enough to stall live evaluations.
const retentionBatchSize = 500

// Retention operation labels, used as bounded metric dimensions.
const (
	retentionOpEvaluations = "evaluations"
	retentionOpFeedback    = "evaluation_feedback"
	retentionOpIdempotency = "idempotency_records"
)
