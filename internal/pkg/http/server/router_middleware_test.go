package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rate-limit headroom for the cases that are not about rate limiting. Config
// validation refuses a non-positive limit, so leaving it zero would put the
// router in a state the service never runs in.
const (
	testRequestsPerMinute = 6000
	testRateLimitBurst    = 600
)

// apiRequestRecords drives one request through the real API middleware chain
// and returns the response plus every structured log record it produced.
//
// It replaces the process-global slog default to capture those records, so
// none of its callers may run in parallel.
func apiRequestRecords(
	t *testing.T,
	config RouterConfig,
	req *http.Request,
) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()

	var buf bytes.Buffer

	original := slog.Default()

	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	if config.Metrics == nil {
		config.Metrics = metrics.New()
	}

	if config.RequestsPerMinute <= 0 {
		config.RequestsPerMinute = testRequestsPerMinute
	}

	if config.RateLimitBurst <= 0 {
		config.RateLimitBurst = testRateLimitBurst
	}

	router := &Router{config: config}

	handler := router.apiMiddleware()(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		// Draining the body is what makes the size limit observable: the
		// limit fails the read, not the routing.
		_, _ = io.Copy(io.Discard, r.Body)

		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	records := []map[string]any{}

	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}

		record := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(line), &record), line)
		records = append(records, record)
	}

	return recorder, records
}

// Every API access-log record has to carry the correlation ID. Wrapping the
// logger outside the request-ID middleware silently drops it, and a request
// that cannot be correlated is not traceable through the rest of the system.
func TestAPIAccessLogCarriesTheRequestID(t *testing.T) {
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, routePolicies, nil,
	)
	req.Pattern = http.MethodGet + " " + routePolicies

	recorder, records := apiRequestRecords(t, RouterConfig{}, req)

	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.NotEmpty(t, records, "the access log emits a completion record")

	completed := findRecord(t, records, "request completed")

	requestID, ok := completed["request_id"].(string)
	require.True(
		t, ok, "the completion record carries request_id: %v", completed,
	)
	assert.NotEmpty(t, requestID)
	assert.Equal(
		t, recorder.Header().Get("X-Request-Id"), requestID,
		"the logged ID is the one the caller was handed back",
	)
}

// A rejected request still has to produce a completion record, or the
// requests an operator most wants to see are the ones missing from the log.
func TestAPIAccessLogRecordsRejectedRequests(t *testing.T) {
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, routePolicies, nil,
	)
	req.Pattern = http.MethodGet + " " + routePolicies

	recorder, records := apiRequestRecords(
		t, RouterConfig{APIToken: "the-token"}, req,
	)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)

	completed := findRecord(t, records, "request completed")
	assert.InDelta(t, float64(http.StatusUnauthorized), completed["status"], 0)
	assert.NotEmpty(t, completed["request_id"])
}

// No log record may carry the caller's body, the configured token, or an
// arbitrary request header.
func TestAPIAccessLogLeaksNeitherBodyNorCredentials(t *testing.T) {
	const (
		token      = "EXAMPLE-TOKEN-DO-NOT-USE"
		bodyMarker = "DISTINCTIVE-BODY-MARKER"
		header     = "DISTINCTIVE-HEADER-MARKER"
	)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, routeEvaluations,
		strings.NewReader(`{"state":"`+bodyMarker+`"}`),
	)
	req.Pattern = http.MethodPost + " " + routeEvaluations
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Custom-Trace", header)

	_, records := apiRequestRecords(t, RouterConfig{APIToken: token}, req)

	require.NotEmpty(t, records)

	encoded, err := json.Marshal(records)
	require.NoError(t, err)

	for label, forbidden := range map[string]string{
		"the configured token": token,
		"the request body":     bodyMarker,
		"an arbitrary header":  header,
	} {
		assert.NotContains(
			t, string(encoded), forbidden,
			"%s must never reach a log record", label,
		)
	}
}

// The contract declares 413 for an oversized body. Answering 400 tells the
// caller their request was malformed, so they retry the same bytes.
func TestOversizedBodyIsRejectedAsPayloadTooLarge(t *testing.T) {
	const limit = 64

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, routeEvaluations,
		strings.NewReader(`{"state":"`+strings.Repeat("A", limit*4)+`"}`),
	)
	req.Pattern = http.MethodPost + " " + routeEvaluations

	recorder, _ := apiRequestRecords(
		t, RouterConfig{MaxRequestBytes: limit}, req,
	)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)

	body := map[string]any{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.NotEmpty(t, body["code"], "the standard envelope carries a code")
}

// A body inside the limit passes through untouched.
func TestBodyWithinTheLimitReachesTheHandler(t *testing.T) {
	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, routeEvaluations,
		strings.NewReader(`{"state":"small"}`),
	)
	req.Pattern = http.MethodPost + " " + routeEvaluations

	recorder, _ := apiRequestRecords(
		t, RouterConfig{MaxRequestBytes: 1 << 20}, req,
	)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func findRecord(
	t *testing.T,
	records []map[string]any,
	message string,
) map[string]any {
	t.Helper()

	for _, record := range records {
		if record["msg"] == message {
			return record
		}
	}

	t.Fatalf("no %q record in %v", message, records)

	return nil
}
