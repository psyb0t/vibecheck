package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/http/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorEnvelope is the shape every middleware failure writes back. Tests
// decode into it instead of comparing raw response bytes.
type errorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// okHandler is the smallest possible terminal handler: it reports success
// and does nothing else, so a test can focus on the middleware in front of
// it.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

const testBearerToken = "s3cr3t-token-value"

func TestBearerAuthWithEmptyTokenIsTheTrustedLoopbackMode(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	})

	handler := server.BearerAuth("")(next)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called, "an empty configured token disables the check")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBearerAuthWithCorrectTokenPasses(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	})

	handler := server.BearerAuth(testBearerToken)(next)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	req.Header.Set("Authorization", "Bearer "+testBearerToken)

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestBearerAuthRejectsEveryWrongCredential(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		header string
	}{
		{"missing authorization header", ""},
		{"wrong scheme", "Basic " + testBearerToken},
		{"wrong token", "Bearer not-the-token"},
		{
			"token is a prefix of the real one",
			"Bearer " + testBearerToken[:len(testBearerToken)-1],
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			called := false
			next := http.HandlerFunc(func(
				w http.ResponseWriter, _ *http.Request,
			) {
				called = true

				w.WriteHeader(http.StatusOK)
			})

			handler := server.BearerAuth(testBearerToken)(next)

			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodGet, "/", nil,
			)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.False(t, called, "next must not run on a rejected token")
			assert.Equal(t, http.StatusUnauthorized, rec.Code)

			var env errorEnvelope

			err := json.Unmarshal(rec.Body.Bytes(), &env)
			require.NoError(t, err)
			assert.NotEmpty(
				t, env.Code, "the 401 body carries a stable code",
			)
		})
	}
}

func TestBodyLimitAllowsABodyAtTheLimit(t *testing.T) {
	t.Parallel()

	const limit = 16

	body := strings.Repeat("a", limit)

	var (
		readErr  error
		readBody []byte
	)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readBody, readErr = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})

	handler := server.BodyLimit(limit)(next)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/", strings.NewReader(body),
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.NoError(t, readErr)
	assert.Equal(t, body, string(readBody))
}

func TestBodyLimitFailsTheReadOverTheLimit(t *testing.T) {
	t.Parallel()

	const limit = 16

	body := strings.Repeat("a", limit+1)

	var readErr error

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)

		w.WriteHeader(http.StatusOK)
	})

	handler := server.BodyLimit(limit)(next)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/", strings.NewReader(body),
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var maxBytesErr *http.MaxBytesError

	assert.ErrorAs(
		t, readErr, &maxBytesErr,
		"the oversized body fails the handler's own read, not the response",
	)
}

func TestRequestIDMintsAValidUUIDWhenNoneIsSupplied(t *testing.T) {
	t.Parallel()

	handler := server.RequestID()(okHandler())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	got := rec.Header().Get(api.HeaderRequestID)
	_, err := uuid.Parse(got)
	assert.NoError(t, err)
}

func TestRequestIDEchoesASuppliedValidUUID(t *testing.T) {
	t.Parallel()

	want := uuid.NewString()

	handler := server.RequestID()(okHandler())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	req.Header.Set(api.HeaderRequestID, want)

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, want, rec.Header().Get(api.HeaderRequestID))
}

func TestRequestIDLogInjectionGuardReplacesNonUUIDValues(t *testing.T) {
	t.Parallel()

	const junk = "'; DROP TABLE requests; -- \n injected"

	handler := server.RequestID()(okHandler())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	req.Header.Set(api.HeaderRequestID, junk)

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	got := rec.Header().Get(api.HeaderRequestID)
	assert.NotEqual(t, junk, got, "the caller's junk must not be echoed")

	_, err := uuid.Parse(got)
	assert.NoError(t, err, "a fresh UUID replaces the rejected value")
}

func TestRequestIDLogInjectionGuardReplacesOverLongValues(t *testing.T) {
	t.Parallel()

	const overLongRequestIDLength = 200

	junk := strings.Repeat("9", overLongRequestIDLength)

	handler := server.RequestID()(okHandler())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	req.Header.Set(api.HeaderRequestID, junk)

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	got := rec.Header().Get(api.HeaderRequestID)
	assert.NotEqual(t, junk, got, "the over-long value must not be echoed")

	_, err := uuid.Parse(got)
	assert.NoError(t, err, "a fresh UUID replaces the rejected value")
}

func TestSecurityHeadersSetsEveryExpectedHeader(t *testing.T) {
	t.Parallel()

	handler := server.SecurityHeaders()(okHandler())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	testCases := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "no-referrer"},
		{"Content-Security-Policy", "default-src 'none'"},
		{"Cache-Control", "no-store"},
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.want, rec.Header().Get(tc.header), tc.header)
	}
}

func TestRecoveryTurnsAPanicIntoTheErrorEnvelope(t *testing.T) {
	t.Parallel()

	panicking := http.HandlerFunc(func(
		http.ResponseWriter, *http.Request,
	) {
		panic("simulated handler panic")
	})

	handler := server.Recovery()(panicking)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	rec := httptest.NewRecorder()

	require.NotPanics(t, func() {
		handler.ServeHTTP(rec, req)
	})

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var env errorEnvelope

	err := json.Unmarshal(rec.Body.Bytes(), &env)
	require.NoError(t, err)
	assert.NotEmpty(t, env.Code)
}

func TestRateLimitAllowsTheBurstThenRejects(t *testing.T) {
	t.Parallel()

	const (
		tinyPerMinute = 1
		tinyBurst     = 1
	)

	limiter := server.NewRateLimiter(tinyPerMinute, tinyBurst)
	handler := server.RateLimit(limiter)(okHandler())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, req)
	assert.Equal(t, http.StatusOK, first.Code, "the burst request passes")

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, req)
	assert.Equal(
		t, http.StatusTooManyRequests, second.Code,
		"the request past the burst is rejected",
	)

	var env errorEnvelope

	err := json.Unmarshal(second.Body.Bytes(), &env)
	require.NoError(t, err)
	assert.NotEmpty(t, env.Code)
}

// TestStatusRecorderDefaultsWriteOnlyResponsesToOK exercises the wrapper
// statusRecorder applies inside AccessLog: a handler that only ever calls
// Write, never WriteHeader, must still be recorded as a 200. The completed
// request log line is the observable proof, since the wrapper's own status
// field is unexported.
//
// It mutates the process-global slog default logger to capture that line,
// so unlike its siblings it does not call t.Parallel().
func TestStatusRecorderDefaultsWriteOnlyResponsesToOK(t *testing.T) {
	var buf bytes.Buffer

	original := slog.Default()

	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("ok"))
		require.NoError(t, err)
	})

	handler := server.AccessLog("test-route")(next)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	var record struct {
		Msg    string `json:"msg"`
		Status int    `json:"status"`
		Route  string `json:"route"`
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.NotEmpty(t, lines)

	found := false

	for _, line := range lines {
		if json.Unmarshal([]byte(line), &record) != nil {
			continue
		}

		if record.Msg == "request completed" {
			found = true

			break
		}
	}

	require.True(t, found, "expected a request completed log line")
	assert.Equal(t, http.StatusOK, record.Status)
	assert.Equal(t, "test-route", record.Route)
}

// TestWrappedResponseWriterSatisfiesFlusherForStreaming is the Flush
// passthrough guard: the wrapper AccessLog puts in front of a handler must
// still satisfy http.Flusher, or streaming transports such as the MCP SSE
// handler would silently stop flushing.
func TestWrappedResponseWriterSatisfiesFlusherForStreaming(t *testing.T) {
	t.Parallel()

	sawFlusher := false

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawFlusher = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	})

	handler := server.AccessLog("test-route")(next)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(
		t, sawFlusher,
		"the wrapped ResponseWriter must still implement http.Flusher",
	)
}
