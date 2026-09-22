package metrics_test

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// findFamily locates a gathered metric family by its fully qualified name.
func findFamily(
	families []*dto.MetricFamily,
	name string,
) *dto.MetricFamily {
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}

	return nil
}

// findMetric locates the one series in family whose label set is exactly
// want, no more and no fewer labels. A nil family is a safe no-match.
func findMetric(
	family *dto.MetricFamily,
	want map[string]string,
) *dto.Metric {
	if family == nil {
		return nil
	}

	for _, metric := range family.GetMetric() {
		if labelsMatch(metric, want) {
			return metric
		}
	}

	return nil
}

func labelsMatch(metric *dto.Metric, want map[string]string) bool {
	pairs := metric.GetLabel()
	if len(pairs) != len(want) {
		return false
	}

	for _, pair := range pairs {
		if want[pair.GetName()] != pair.GetValue() {
			return false
		}
	}

	return true
}

func gather(t *testing.T, m *metrics.Metrics) []*dto.MetricFamily {
	t.Helper()

	families, err := m.Registry().Gather()
	require.NoError(t, err)

	return families
}

func TestNewRegistersWithoutPanicking(t *testing.T) {
	t.Parallel()

	var m *metrics.Metrics

	require.NotPanics(t, func() {
		m = metrics.New()
	})

	require.NotNil(t, m.Registry())

	families, err := m.Registry().Gather()
	require.NoError(t, err)
	assert.NotEmpty(t, families,
		"the go/process collectors alone must produce output")
}

func TestObserveHTTPRecordsRequestAndDuration(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.ObserveHTTP("GET", "/v1/evaluations", 200, 0.42)

	families := gather(t, m)

	requests := findFamily(families, "vibecheck_http_requests_total")
	requestMetric := findMetric(requests, map[string]string{
		"method": "GET",
		"route":  "/v1/evaluations",
		"status": "200",
	})
	require.NotNil(t, requestMetric,
		"expected exactly the method/route/status label set")
	assert.Equal(t, float64(1), requestMetric.GetCounter().GetValue())

	duration := findFamily(
		families, "vibecheck_http_request_duration_seconds",
	)
	durationMetric := findMetric(duration, map[string]string{
		"method": "GET",
		"route":  "/v1/evaluations",
	})
	require.NotNil(t, durationMetric)

	histogram := durationMetric.GetHistogram()
	assert.Equal(t, uint64(1), histogram.GetSampleCount())
	assert.InDelta(t, 0.42, histogram.GetSampleSum(), 1e-9)
}

// TestObserveProviderAttemptBucketsStatusToBoundedClasses is the
// high-cardinality guard on statusClass: whatever status code the upstream
// returns, it must collapse into one of a small, fixed set of series
// instead of minting a new one per code.
func TestObserveProviderAttemptBucketsStatusToBoundedClasses(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		status  int
		failed  bool
		class   string
		outcome string
	}{
		{
			name: "2xx success", status: 200, failed: false,
			class: "2xx", outcome: "success",
		},
		{
			name: "5xx failure", status: 503, failed: true,
			class: "5xx", outcome: "error",
		},
		{
			name: "no response received", status: 0, failed: true,
			class: "none", outcome: "error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := metrics.New()
			m.ObserveProviderAttempt(tc.status, tc.failed, 0.1)

			families := gather(t, m)
			requests := findFamily(
				families, "vibecheck_provider_requests_total",
			)
			metric := findMetric(requests, map[string]string{
				"status_class":  tc.class,
				"outcome_class": tc.outcome,
			})
			require.NotNil(t, metric)
			assert.Equal(t, float64(1), metric.GetCounter().GetValue())
		})
	}
}

func TestObserveMCPToolRecordsSuccessAndErrorSeparately(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.ObserveMCPTool("policy_evaluate", metrics.OutcomeClassSuccess, 0.1)
	m.ObserveMCPTool("policy_evaluate", metrics.OutcomeClassError, 0.2)

	families := gather(t, m)
	calls := findFamily(families, "vibecheck_mcp_tool_calls_total")

	success := findMetric(calls, map[string]string{
		"tool":          "policy_evaluate",
		"outcome_class": "success",
	})
	require.NotNil(t, success)
	assert.Equal(t, float64(1), success.GetCounter().GetValue())

	failure := findMetric(calls, map[string]string{
		"tool":          "policy_evaluate",
		"outcome_class": "error",
	})
	require.NotNil(t, failure)
	assert.Equal(t, float64(1), failure.GetCounter().GetValue())

	duration := findFamily(families, "vibecheck_mcp_tool_duration_seconds")
	durationMetric := findMetric(
		duration, map[string]string{"tool": "policy_evaluate"},
	)
	require.NotNil(t, durationMetric)

	histogram := durationMetric.GetHistogram()
	assert.Equal(t, uint64(2), histogram.GetSampleCount())
	assert.InDelta(t, 0.3, histogram.GetSampleSum(), 1e-9)
}

// TestObserveTokensWithEmptyModelRecordsNothing guards against an empty
// model label minting its own series: ObserveTokens must be a no-op when
// the model that answered is unknown.
func TestObserveTokensWithEmptyModelRecordsNothing(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.ObserveTokens("", 10, 20)

	families := gather(t, m)

	input := findFamily(families, "vibecheck_model_input_tokens_total")
	assert.Nil(t, findMetric(input, map[string]string{"model": ""}),
		"an empty model must never mint a label series")

	output := findFamily(families, "vibecheck_model_output_tokens_total")
	assert.Nil(t, findMetric(output, map[string]string{"model": ""}))
}

func TestSetBuildInfo(t *testing.T) {
	t.Parallel()

	m := metrics.New()
	m.SetBuildInfo("v1.2.3", "abcdef1")

	families := gather(t, m)
	build := findFamily(families, "vibecheck_build_info")
	metric := findMetric(build, map[string]string{
		"version": "v1.2.3",
		"commit":  "abcdef1",
	})
	require.NotNil(t, metric)
	assert.Equal(t, float64(1), metric.GetGauge().GetValue())
}

func TestObserveRetentionRun(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		failed  bool
		deleted map[string]int64
		result  string
	}{
		{
			name:   "success",
			failed: false,
			deleted: map[string]int64{
				"evaluations": 5, "idempotency_keys": 2,
			},
			result: "success",
		},
		{
			name:    "failure",
			failed:  true,
			deleted: map[string]int64{"evaluations": 1},
			result:  "error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := metrics.New()
			m.ObserveRetentionRun(tc.failed, tc.deleted)

			families := gather(t, m)

			runs := findFamily(families, "vibecheck_retention_runs_total")
			runMetric := findMetric(
				runs, map[string]string{"result": tc.result},
			)
			require.NotNil(t, runMetric)
			assert.Equal(t, float64(1), runMetric.GetCounter().GetValue())

			deletedFamily := findFamily(
				families, "vibecheck_retention_rows_deleted_total",
			)

			for operation, count := range tc.deleted {
				deletedMetric := findMetric(deletedFamily, map[string]string{
					"operation": operation,
				})
				require.NotNil(t, deletedMetric)
				assert.Equal(
					t, float64(count),
					deletedMetric.GetCounter().GetValue(),
				)
			}
		})
	}
}

// TestNewInstancesHaveIndependentRegistries confirms Metrics keeps no
// process-global state: a second New() must never observe what the first
// one recorded.
func TestNewInstancesHaveIndependentRegistries(t *testing.T) {
	t.Parallel()

	first := metrics.New()
	second := metrics.New()

	first.ObserveHTTP("GET", "/only-on-first", 200, 0.1)

	secondFamilies := gather(t, second)
	requests := findFamily(
		secondFamilies, "vibecheck_http_requests_total",
	)
	assert.Nil(t, findMetric(requests, map[string]string{
		"method": "GET",
		"route":  "/only-on-first",
		"status": "200",
	}), "a second Metrics instance must not see the first's series")

	assert.NotSame(t, first.Registry(), second.Registry())
}
