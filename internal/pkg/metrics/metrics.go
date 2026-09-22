// Package metrics holds Vibecheck's Prometheus instruments.
//
// Every label here is bounded. Evaluation IDs, request IDs, policy names,
// policy hashes, caller metadata, and raw paths are never labels: they are
// unbounded in practice, and one high-cardinality label is enough to take a
// Prometheus server down. The exact identity of a decision lives in the
// structured logs and the audit row, which is where it belongs.
package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// namespace prefixes every instrument, and the subsystems split them by the
// concern they measure.
const (
	namespace = "vibecheck"

	subsystemHTTP       = "http"
	subsystemMCP        = "mcp"
	subsystemProvider   = "provider"
	subsystemModel      = "model"
	subsystemEvaluation = "evaluation"
	subsystemDB         = "db"
	subsystemRetention  = "retention"
	subsystemBuild      = "build"
)

// Label names, shared so a typo cannot create a parallel series.
const (
	labelMethod        = "method"
	labelRoute         = "route"
	labelStatus        = "status"
	labelTool          = "tool"
	labelOutcomeClass  = "outcome_class"
	labelStatusClass   = "status_class"
	labelModel         = "model"
	labelPolicyKind    = "policy_kind"
	labelOutcome       = "outcome"
	labelExecutionPath = "execution_path"
	labelOperation     = "operation"
	labelResult        = "result"
	labelVersion       = "version"
	labelCommit        = "commit"
)

// statusClassDivisor turns a status code into its class, so the label
// stays a five-value domain instead of one series per code.
const statusClassDivisor = 100

// attemptBuckets counts physical provider attempts. The ceiling sits above
// any sane max-attempts setting, so a misconfiguration stays visible instead
// of piling into +Inf.
//
// It is a function for the same reason durationBuckets is: prometheus keeps
// the slice it is handed.
func attemptBuckets() []float64 {
	return []float64{1, 2, 3, 4, 5, 10}
}

// OutcomeClass is the bounded success or failure label shared by the MCP and
// provider instruments.
type OutcomeClass string

const (
	OutcomeClassSuccess OutcomeClass = "success"
	OutcomeClassError   OutcomeClass = "error"
)

func (c OutcomeClass) String() string {
	return string(c)
}

// durationBuckets is tuned for a synchronous decision service. The slowest
// bucket sits well past the provider timeout, so a stuck call is still
// visible rather than piling into +Inf.
//
// It is a function because prometheus keeps the slice it is handed, and a
// shared package-level slice would let one instrument's bucket list be
// mutated out from under every other.
func durationBuckets() []float64 {
	return []float64{
		0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
	}
}

// Metrics owns every instrument and the registry they live in.
//
// It is passed explicitly rather than kept in a package global, so a test can
// build an isolated registry and assert on it without touching process state.
type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	mcpCalls    *prometheus.CounterVec
	mcpDuration *prometheus.HistogramVec
	mcpInFlight prometheus.Gauge

	providerRequests *prometheus.CounterVec
	providerDuration *prometheus.HistogramVec
	providerAttempts prometheus.Histogram
	providerInFlight prometheus.Gauge

	modelInputTokens  *prometheus.CounterVec
	modelOutputTokens *prometheus.CounterVec

	evaluations        *prometheus.CounterVec
	evaluationDuration *prometheus.HistogramVec

	dbOperations *prometheus.CounterVec
	dbDuration   *prometheus.HistogramVec

	retentionRuns    *prometheus.CounterVec
	retentionDeleted *prometheus.CounterVec

	buildInfo *prometheus.GaugeVec
}

// New builds every instrument against a fresh registry.
//
// The registry is private to this value rather than the Prometheus default
// one, so the process exposes exactly these series and a test can build an
// independent set without colliding on a duplicate registration.
func New() *Metrics {
	metrics := &Metrics{registry: prometheus.NewRegistry()}

	metrics.initHTTP()
	metrics.initMCP()
	metrics.initProvider()
	metrics.initModel()
	metrics.initEvaluation()
	metrics.initDB()
	metrics.initRetention()
	metrics.initBuild()

	metrics.mustRegister()

	return metrics
}

func (m *Metrics) initHTTP() {
	m.httpRequests = counterVec(
		subsystemHTTP, "requests_total",
		"HTTP requests by method, route template, and status.",
		labelMethod, labelRoute, labelStatus,
	)
	m.httpDuration = histogramVec(
		subsystemHTTP, "request_duration_seconds",
		"HTTP request duration by method and route template.",
		labelMethod, labelRoute,
	)
	m.httpInFlight = gauge(
		subsystemHTTP, "in_flight_requests",
		"HTTP requests currently being served.",
	)
}

func (m *Metrics) initMCP() {
	m.mcpCalls = counterVec(
		subsystemMCP, "tool_calls_total",
		"MCP tool calls by tool and outcome class.",
		labelTool, labelOutcomeClass,
	)
	m.mcpDuration = histogramVec(
		subsystemMCP, "tool_duration_seconds",
		"MCP tool duration by tool.", labelTool,
	)
	m.mcpInFlight = gauge(
		subsystemMCP, "in_flight_calls", "MCP tool calls currently running.",
	)
}

func (m *Metrics) initProvider() {
	m.providerRequests = counterVec(
		subsystemProvider, "requests_total",
		"Provider attempts by HTTP status class and outcome class.",
		labelStatusClass, labelOutcomeClass,
	)
	m.providerDuration = histogramVec(
		subsystemProvider, "request_duration_seconds",
		"Provider attempt duration by outcome class.", labelOutcomeClass,
	)
	m.providerAttempts = histogram(
		subsystemProvider, "attempts_per_call",
		"Physical attempts spent on one provider call.",
		attemptBuckets(),
	)
	m.providerInFlight = gauge(
		subsystemProvider, "in_flight_requests",
		"Provider requests currently in flight.",
	)
}

func (m *Metrics) initModel() {
	m.modelInputTokens = counterVec(
		subsystemModel, "input_tokens_total",
		"Input tokens billed, by resolved model.", labelModel,
	)
	m.modelOutputTokens = counterVec(
		subsystemModel, "output_tokens_total",
		"Output tokens reported, by resolved model.", labelModel,
	)
}

func (m *Metrics) initEvaluation() {
	m.evaluations = counterVec(
		subsystemEvaluation, "total",
		"Evaluations by policy kind, outcome, and execution path.",
		labelPolicyKind, labelOutcome, labelExecutionPath,
	)
	m.evaluationDuration = histogramVec(
		subsystemEvaluation, "duration_seconds",
		"End to end evaluation duration by policy kind and execution path.",
		labelPolicyKind, labelExecutionPath,
	)
}

func (m *Metrics) initDB() {
	m.dbOperations = counterVec(
		subsystemDB, "operations_total",
		"Database operations by operation and outcome class.",
		labelOperation, labelOutcomeClass,
	)
	m.dbDuration = histogramVec(
		subsystemDB, "operation_duration_seconds",
		"Database operation duration by operation.", labelOperation,
	)
}

func (m *Metrics) initRetention() {
	m.retentionRuns = counterVec(
		subsystemRetention, "runs_total",
		"Retention cleanup runs by result.", labelResult,
	)
	m.retentionDeleted = counterVec(
		subsystemRetention, "rows_deleted_total",
		"Rows removed by retention cleanup, by operation.", labelOperation,
	)
}

func (m *Metrics) initBuild() {
	m.buildInfo = gaugeVec(
		subsystemBuild, "info", "Build identity, always 1.",
		labelVersion, labelCommit,
	)
}

func (m *Metrics) mustRegister() {
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),

		m.httpRequests, m.httpDuration, m.httpInFlight,
		m.mcpCalls, m.mcpDuration, m.mcpInFlight,
		m.providerRequests, m.providerDuration, m.providerAttempts,
		m.providerInFlight,
		m.modelInputTokens, m.modelOutputTokens,
		m.evaluations, m.evaluationDuration,
		m.dbOperations, m.dbDuration,
		m.retentionRuns, m.retentionDeleted,
		m.buildInfo,
	)
}

// Registry exposes the registry so the internal listener can serve it.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// SetBuildInfo records the running build. Version and commit are process
// identity, fixed at start, so they are a safe label pair.
func (m *Metrics) SetBuildInfo(version, commit string) {
	m.buildInfo.WithLabelValues(version, commit).Set(1)
}

// ObserveHTTP records one served request. route is the route template, never
// the raw path, so a scanner hitting random URLs cannot mint series.
func (m *Metrics) ObserveHTTP(
	method, route string,
	status int,
	seconds float64,
) {
	m.httpRequests.WithLabelValues(
		method, route, strconv.Itoa(status),
	).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(seconds)
}

// HTTPInFlight returns the gauge the middleware increments around a request.
func (m *Metrics) HTTPInFlight() prometheus.Gauge {
	return m.httpInFlight
}

// ObserveMCPTool records one MCP tool call.
func (m *Metrics) ObserveMCPTool(
	tool string,
	class OutcomeClass,
	seconds float64,
) {
	m.mcpCalls.WithLabelValues(tool, class.String()).Inc()
	m.mcpDuration.WithLabelValues(tool).Observe(seconds)
}

// MCPInFlight returns the gauge the MCP layer increments around a call.
func (m *Metrics) MCPInFlight() prometheus.Gauge {
	return m.mcpInFlight
}

// ObserveProviderAttempt records one physical provider request. The status is
// bucketed into a class so an unexpected code cannot create a new series.
func (m *Metrics) ObserveProviderAttempt(
	status int,
	failed bool,
	seconds float64,
) {
	class := OutcomeClassSuccess
	if failed {
		class = OutcomeClassError
	}

	m.providerRequests.WithLabelValues(
		statusClass(status), class.String(),
	).Inc()
	m.providerDuration.WithLabelValues(class.String()).Observe(seconds)
}

// ObserveProviderCall records how many attempts one logical call took.
func (m *Metrics) ObserveProviderCall(attempts int) {
	m.providerAttempts.Observe(float64(attempts))
}

// ProviderInFlight returns the gauge the adapter increments around a call.
func (m *Metrics) ProviderInFlight() prometheus.Gauge {
	return m.providerInFlight
}

// ProviderCallStarted increments the logical provider in-flight count.
func (m *Metrics) ProviderCallStarted() {
	m.providerInFlight.Inc()
}

// ProviderCallFinished decrements the logical provider in-flight count.
func (m *Metrics) ProviderCallFinished() {
	m.providerInFlight.Dec()
}

// ObserveTokens records billed tokens against the model that answered.
func (m *Metrics) ObserveTokens(model string, input, output int) {
	if model == "" {
		return
	}

	m.modelInputTokens.WithLabelValues(model).Add(float64(input))
	m.modelOutputTokens.WithLabelValues(model).Add(float64(output))
}

// ObserveEvaluation records one completed evaluation.
func (m *Metrics) ObserveEvaluation(
	policyKind, outcome, executionPath string,
	seconds float64,
) {
	m.evaluations.WithLabelValues(policyKind, outcome, executionPath).Inc()
	m.evaluationDuration.WithLabelValues(policyKind, executionPath).
		Observe(seconds)
}

// ObserveDB records one database operation.
func (m *Metrics) ObserveDB(operation string, failed bool, seconds float64) {
	class := OutcomeClassSuccess
	if failed {
		class = OutcomeClassError
	}

	m.dbOperations.WithLabelValues(operation, class.String()).Inc()
	m.dbDuration.WithLabelValues(operation).Observe(seconds)
}

// ObserveRetentionRun records one cleanup pass and what it removed.
func (m *Metrics) ObserveRetentionRun(
	failed bool,
	deletedByOperation map[string]int64,
) {
	result := OutcomeClassSuccess
	if failed {
		result = OutcomeClassError
	}

	m.retentionRuns.WithLabelValues(result.String()).Inc()

	for operation, count := range deletedByOperation {
		m.retentionDeleted.WithLabelValues(operation).Add(float64(count))
	}
}

// statusClass buckets an HTTP status into 2xx style classes. A zero status
// means no response arrived.
func statusClass(status int) string {
	if status == 0 {
		return "none"
	}

	return strconv.Itoa(status/statusClassDivisor) + "xx"
}

func counterVec(
	subsystem, name, help string,
	labels ...string,
) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, labels)
}

func histogramVec(
	subsystem, name, help string,
	labels ...string,
) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Buckets:   durationBuckets(),
	}, labels)
}

func histogram(
	subsystem, name, help string,
	buckets []float64,
) prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	})
}

func gauge(subsystem, name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	})
}

func gaugeVec(
	subsystem, name, help string,
	labels ...string,
) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, labels)
}
