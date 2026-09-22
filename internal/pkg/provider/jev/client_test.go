package jev_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	"github.com/psyb0t/vibecheck/internal/pkg/provider/jev"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// #nosec G101 -- A literal test token. The adapter needs some bearer
	// value to put on the wire so the assertion can read it back.
	testAPIKey     = "test-key-not-a-real-credential"
	questionRisky  = "risky"
	questionSpread = "spread"
	questionClass  = "actionClass"
	answeredModel  = "jev-1.13.0"
)

// scriptedProvider is a narrow mock of the TypeSafe HTTP boundary. Each call
// pops the next scripted reply, so a test states the exact sequence a retry
// path should walk.
type scriptedProvider struct {
	t *testing.T

	mu      sync.Mutex
	replies []reply
	calls   []recordedCall
}

type reply struct {
	status  int
	body    string
	headers map[string]string

	// hijack lets a reply close the connection without answering, which is
	// how the transport-failure path is exercised.
	hijack bool
}

type recordedCall struct {
	authorization string
	path          string
	body          map[string]any
}

func newScriptedProvider(t *testing.T, replies ...reply) *scriptedProvider {
	t.Helper()

	return &scriptedProvider{t: t, replies: replies}
}

func (s *scriptedProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	decoded := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
		decoded = map[string]any{}
	}

	s.calls = append(s.calls, recordedCall{
		authorization: r.Header.Get("Authorization"),
		path:          r.URL.Path,
		body:          decoded,
	})

	if len(s.replies) == 0 {
		w.WriteHeader(http.StatusTeapot)

		return
	}

	next := s.replies[0]
	s.replies = s.replies[1:]

	if next.hijack {
		hijacker, ok := w.(http.Hijacker)
		require.True(s.t, ok, "test server must support hijacking")

		conn, _, err := hijacker.Hijack()
		require.NoError(s.t, err)
		require.NoError(s.t, conn.Close())

		return
	}

	for name, value := range next.headers {
		w.Header().Set(name, value)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(next.status)

	if next.body != "" {
		_, err := w.Write([]byte(next.body))
		require.NoError(s.t, err)
	}
}

func (s *scriptedProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.calls)
}

func (s *scriptedProvider) call(index int) recordedCall {
	s.mu.Lock()
	defer s.mu.Unlock()

	require.Greater(s.t, len(s.calls), index)

	return s.calls[index]
}

// successBody is a well-formed response covering all three answer types.
const successBody = `{
  "model": "jev-1.13.0",
  "answers": {
    "risky": {"type": "noul", "noul": 0.25},
    "spread": {
      "type": "score",
      "score": 1.5,
      "confidence": 0.88,
      "legend": {"0": "low", "1": "medium", "2": "high"},
      "probabilities": {"0": 0.1, "1": 0.4, "2": 0.5}
    },
    "actionClass": {
      "type": "choice",
      "choice": "read",
      "confidence": 0.77,
      "probabilities": {"read": 0.8, "write": 0.2}
    }
  },
  "usage": {"input_tokens": 392, "output_tokens": 65}
}`

func testRequest() provider.Request {
	return provider.Request{
		State: "a synthetic state used only by this test",
		Questions: []provider.Question{
			{
				ID:           questionRisky,
				Type:         decision.QuestionTypeNoul,
				Instructions: "Is this risky?",
			},
			{
				ID:            questionSpread,
				Type:          decision.QuestionTypeScore,
				Instructions:  "How wide is the impact?",
				ScoreCriteria: []string{"low", "medium", "high"},
			},
			{
				ID:           questionClass,
				Type:         decision.QuestionTypeChoice,
				Instructions: "Which class?",
				ChoiceCriteria: map[string]string{
					"read":  "reads state",
					"write": "changes state",
				},
			},
		},
	}
}

// newClient wires a client against a scripted server. Backoff is compressed
// so retry tests stay fast without weakening what they assert.
func newClient(
	t *testing.T,
	script *scriptedProvider,
	adjust func(*jev.Config),
	options ...jev.Option,
) *jev.Client {
	t.Helper()

	server := httptest.NewServer(script)
	t.Cleanup(server.Close)

	config := jev.Config{
		BaseURL:     server.URL,
		APIKey:      testAPIKey,
		BackoffBase: time.Millisecond,
		BackoffMax:  2 * time.Millisecond,
	}

	if adjust != nil {
		adjust(&config)
	}

	client, err := jev.New(config, options...)
	require.NoError(t, err)

	return client
}

func TestEvaluateDecodesEveryAnswerType(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, nil)

	response, err := client.Evaluate(t.Context(), testRequest())
	require.NoError(t, err)

	assert.Equal(t, answeredModel, response.Model)
	assert.Equal(t, 392, response.Usage.InputTokens)
	assert.Equal(t, 65, response.Usage.OutputTokens)
	assert.Equal(t, 1, response.AttemptCount())
	assert.Positive(t, response.Duration)

	noul := response.Answers[questionRisky]
	assert.Equal(t, decision.QuestionTypeNoul, noul.Type)
	assert.InDelta(t, 0.25, noul.Noul, 0)
	assert.False(t, noul.HasConfidence, "a noul answer carries no confidence")

	score := response.Answers[questionSpread]
	assert.Equal(t, decision.QuestionTypeScore, score.Type)
	assert.InDelta(t, 1.5, score.Score, 0)
	assert.True(t, score.HasConfidence)
	assert.InDelta(t, 0.88, score.Confidence, 0)
	assert.Equal(t, "medium", score.Legend["1"])
	assert.InDelta(t, 0.5, score.Probabilities["2"], 0)

	choice := response.Answers[questionClass]
	assert.Equal(t, decision.QuestionTypeChoice, choice.Type)
	assert.Equal(t, "read", choice.Choice)
	assert.True(t, choice.HasConfidence)
	assert.InDelta(t, 0.8, choice.Probabilities["read"], 0)
}

func TestEvaluateSendsTheDocumentedRequestShape(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, nil)

	_, err := client.Evaluate(t.Context(), testRequest())
	require.NoError(t, err)

	call := script.call(0)
	assert.Equal(t, jev.SystemOnePath, call.path)
	assert.Equal(t, "Bearer "+testAPIKey, call.authorization)
	assert.Equal(t, jev.DefaultModel, call.body["model"])
	assert.Equal(
		t,
		"a synthetic state used only by this test",
		call.body["state"],
	)

	questions, ok := call.body["questions"].(map[string]any)
	require.True(t, ok)
	require.Len(t, questions, 3)

	noul, ok := questions[questionRisky].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "noul", noul["type"])
	assert.NotContains(t, noul, "criteria", "a noul question sends no criteria")

	score, ok := questions[questionSpread].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"low", "medium", "high"}, score["criteria"],
		"score criteria are an ordered list because the index is the level")

	choice, ok := questions[questionClass].(map[string]any)
	require.True(t, ok)
	assert.Equal(t,
		map[string]any{"read": "reads state", "write": "changes state"},
		choice["criteria"],
		"choice criteria are a map of option to description")
}

func TestEvaluateUsesTheRequestedModelOverTheDefault(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, nil)

	request := testRequest()
	request.Model = "jev-1.13.0"

	_, err := client.Evaluate(t.Context(), request)
	require.NoError(t, err)

	assert.Equal(t, "jev-1.13.0", script.call(0).body["model"])
}

func TestEvaluateRetriesRetryableStates(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		first reply
	}{
		{
			name: "rate limited",
			first: reply{
				status: http.StatusTooManyRequests,
				body:   `{"error":"slow down"}`,
			},
		},
		{
			name:  "overloaded",
			first: reply{status: 529, body: `{"error":"overloaded"}`},
		},
		{
			name: "server error",
			first: reply{
				status: http.StatusInternalServerError,
				body:   `{"error":"boom"}`,
			},
		},
		{
			name:  "bad gateway",
			first: reply{status: http.StatusBadGateway},
		},
		{
			name:  "connection dropped before a response",
			first: reply{hijack: true},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := newScriptedProvider(
				t, tc.first, reply{status: http.StatusOK, body: successBody},
			)
			client := newClient(t, script, nil)

			response, err := client.Evaluate(t.Context(), testRequest())
			require.NoError(t, err)

			assert.Equal(t, 2, response.AttemptCount())
			assert.Equal(t, 2, script.callCount())
			require.Len(t, response.Attempts, 2)
			assert.Error(t, response.Attempts[0].Err,
				"the failed attempt is kept in the audit trail")
			assert.NoError(t, response.Attempts[1].Err)
		})
	}
}

func TestEvaluateDoesNotRetryTerminalStates(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		reply   reply
		wantErr error
	}{
		{
			name:    "unauthorized",
			reply:   reply{status: http.StatusUnauthorized},
			wantErr: commerr.ErrNotAuthenticated,
		},
		{
			name:    "forbidden",
			reply:   reply{status: http.StatusForbidden},
			wantErr: commerr.ErrNotAuthenticated,
		},
		{
			name:    "unprocessable entity",
			reply:   reply{status: http.StatusUnprocessableEntity},
			wantErr: commerr.ErrValidationFailed,
		},
		{
			name:    "bad request",
			reply:   reply{status: http.StatusBadRequest},
			wantErr: commerr.ErrValidationFailed,
		},
		{
			name: "malformed body",
			reply: reply{
				status: http.StatusOK,
				body:   `{"model":"jev-1.13.0","answers":{"risky":{"type":"noul"}}}`, //nolint:lll // one string literal; splitting it would change the text
			},
			wantErr: commerr.ErrParseFailed,
		},
		{
			name: "response names no model",
			reply: reply{
				status: http.StatusOK,
				body:   `{"answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`, //nolint:lll // one string literal; splitting it would change the text
			},
			wantErr: commerr.ErrParseFailed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := newScriptedProvider(t, tc.reply)
			client := newClient(t, script, nil)

			_, err := client.Evaluate(t.Context(), testRequest())
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, 1, script.callCount(),
				"a terminal failure must not spend another provider call")
		})
	}
}

func TestEvaluateStopsAtTheAttemptCeiling(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusInternalServerError},
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, func(c *jev.Config) { c.MaxAttempts = 3 })

	_, err := client.Evaluate(t.Context(), testRequest())
	require.ErrorIs(t, err, commerr.ErrUnavailable)
	assert.Equal(t, 3, script.callCount())
}

func TestEvaluateHonorsRetryAfter(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		headers map[string]string
		wantMin time.Duration
	}{
		{
			name:    "seconds form",
			headers: map[string]string{"Retry-After": "1"},
			wantMin: time.Second,
		},
		{
			name:    "milliseconds form",
			headers: map[string]string{"retry-after-ms": "250"},
			wantMin: 250 * time.Millisecond,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := newScriptedProvider(
				t,
				reply{status: http.StatusTooManyRequests, headers: tc.headers},
				reply{status: http.StatusOK, body: successBody},
			)
			client := newClient(t, script, nil)

			startedAt := time.Now()
			_, err := client.Evaluate(t.Context(), testRequest())
			require.NoError(t, err)

			assert.GreaterOrEqual(t, time.Since(startedAt), tc.wantMin,
				"the provider's own requested delay must win over the backoff curve") //nolint:lll // one string literal; splitting it would change the text
		})
	}
}

func TestEvaluateCapsAnAbsurdRetryAfter(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{
			status:  http.StatusTooManyRequests,
			headers: map[string]string{"Retry-After": "600"},
		},
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, func(c *jev.Config) {
		c.MaxElapsed = 50 * time.Millisecond
	})

	startedAt := time.Now()
	_, err := client.Evaluate(t.Context(), testRequest())
	elapsed := time.Since(startedAt)

	// Either the elapsed budget cut the series short or the wait was capped.
	// Both are acceptable; parking the caller for ten minutes is not.
	assert.Less(t, elapsed, jev.MaxRetryAfterWait,
		"a provider must not be able to park an inbound request indefinitely")

	if err == nil {
		assert.Equal(t, 2, script.callCount())
	}
}

func TestEvaluateStopsRetryingWhenTheCallerCancels(t *testing.T) {
	t.Parallel()

	// The server signals once it has served the first, failing reply. The
	// test cancels on that signal rather than on a timer, so the assertion
	// does not race the first round trip.
	served := make(chan struct{}, 1)

	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)

			select {
			case served <- struct{}{}:
			default:
			}
		},
	))
	t.Cleanup(server.Close)

	client, err := jev.New(jev.Config{
		BaseURL: server.URL,
		APIKey:  testAPIKey,
		// A long backoff guarantees the call is parked between attempts
		// when the cancellation lands.
		BackoffBase: 10 * time.Second,
		BackoffMax:  10 * time.Second,
		MaxElapsed:  time.Minute,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() {
		<-served
		cancel()
	}()

	_, err = client.Evaluate(ctx, testRequest())
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), calls.Load(),
		"a cancelled call must not spend another provider request")
}

func TestEvaluateRejectsAnOversizedResponse(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(t, reply{
		status: http.StatusOK,
		body:   `{"model":"jev-1.13.0","answers":{},"usage":{}, "pad":"` + strings.Repeat("x", 4096) + `"}`, //nolint:lll // one string literal; splitting it would change the text
	})
	client := newClient(
		t,
		script,
		func(c *jev.Config) { c.MaxResponseBytes = 256 },
	)

	_, err := client.Evaluate(t.Context(), testRequest())
	require.ErrorIs(t, err, provider.ErrResponseTooLarge)
	assert.Equal(t, 1, script.callCount(),
		"an oversized response is the provider breaking its contract, not a transient fault") //nolint:lll // one string literal; splitting it would change the text
}

func TestEvaluateRefusesToCallWithoutACredential(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, func(c *jev.Config) { c.APIKey = "" })

	_, err := client.Evaluate(t.Context(), testRequest())
	require.ErrorIs(t, err, commerr.ErrRequiredConfigValueNotSet)
	assert.Equal(t, 0, script.callCount())
}

func TestEvaluateRejectsAnEmptyQuestionSet(t *testing.T) {
	t.Parallel()

	script := newScriptedProvider(
		t,
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, nil)

	_, err := client.Evaluate(t.Context(), provider.Request{State: "x"})
	require.ErrorIs(t, err, commerr.ErrInvalidArgument)
	assert.Equal(t, 0, script.callCount())
}

func TestEvaluateReportsEveryAttemptToTheObserver(t *testing.T) {
	t.Parallel()

	var observed []provider.Attempt

	var mu sync.Mutex

	script := newScriptedProvider(
		t,
		reply{status: http.StatusTooManyRequests},
		reply{status: http.StatusOK, body: successBody},
	)
	client := newClient(t, script, nil, jev.WithAttemptObserver(
		func(attempt provider.Attempt) {
			mu.Lock()
			defer mu.Unlock()

			observed = append(observed, attempt)
		},
	))

	_, err := client.Evaluate(t.Context(), testRequest())
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, observed, 2)
	assert.Equal(t, 1, observed[0].Number)
	assert.Equal(t, http.StatusTooManyRequests, observed[0].StatusCode)
	assert.Error(t, observed[0].Err)
	assert.Equal(t, 2, observed[1].Number)
	assert.Equal(t, http.StatusOK, observed[1].StatusCode)
	assert.NoError(t, observed[1].Err)
}

func TestEvaluateBoundsConcurrency(t *testing.T) {
	t.Parallel()

	const (
		concurrencyLimit = 2
		callers          = 8
	)

	var (
		inFlight atomic.Int32
		peak     atomic.Int32
	)

	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			current := inFlight.Add(1)

			for {
				observed := peak.Load()
				if current <= observed || peak.CompareAndSwap(
					observed,
					current,
				) {
					break
				}
			}

			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)

			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(successBody))
			require.NoError(t, err)
		},
	))
	t.Cleanup(server.Close)

	client, err := jev.New(jev.Config{
		BaseURL:     server.URL,
		APIKey:      testAPIKey,
		Concurrency: concurrencyLimit,
	})
	require.NoError(t, err)

	group := sync.WaitGroup{}
	for range callers {
		group.Go(func() {
			_, evalErr := client.Evaluate(t.Context(), testRequest())
			assert.NoError(t, evalErr)
		})
	}

	group.Wait()

	assert.LessOrEqual(t, int(peak.Load()), concurrencyLimit,
		"the adapter must never exceed its configured concurrency")
}

func TestEvaluateDoesNotLeakTheProviderResponseBody(t *testing.T) {
	t.Parallel()

	// #nosec G101 -- Not a credential. This marker stands in for caller
	// state echoed back in a provider error body, and the test asserts it
	// never reaches a Vibecheck error or log line.
	const secretish = "STATE-ECHOED-BACK-BY-THE-PROVIDER"

	script := newScriptedProvider(t, reply{
		status: http.StatusUnprocessableEntity,
		body:   `{"error":"rejected ` + secretish + `"}`,
	})
	client := newClient(t, script, nil)

	_, err := client.Evaluate(t.Context(), testRequest())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secretish,
		"a provider error body can echo request content and must not reach the caller") //nolint:lll // one string literal; splitting it would change the text
}

func TestNewRejectsUnusableConfiguration(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		config jev.Config
	}{
		{
			name: "backoff maximum below the base",
			config: jev.Config{
				BaseURL:     "https://example.invalid",
				BackoffBase: 2 * time.Second,
				BackoffMax:  time.Second,
			},
		},
		{
			name:   "base URL that is not absolute",
			config: jev.Config{BaseURL: "/v1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := jev.New(tc.config)
			require.Error(t, err)
		})
	}
}

func TestConfigDefaultsFillEveryUnsetField(t *testing.T) {
	t.Parallel()

	config := jev.Config{}.WithDefaults()

	assert.Equal(t, jev.DefaultBaseURL, config.BaseURL)
	assert.Equal(t, jev.DefaultModel, config.DefaultModel)
	assert.Equal(t, jev.DefaultTimeout, config.Timeout)
	assert.Equal(t, int64(jev.DefaultMaxResponseBytes), config.MaxResponseBytes)
	assert.Equal(t, jev.DefaultConcurrency, config.Concurrency)
	assert.Equal(t, jev.DefaultMaxAttempts, config.MaxAttempts)
	assert.Equal(t, jev.DefaultBackoffBase, config.BackoffBase)
	assert.Equal(t, jev.DefaultBackoffMax, config.BackoffMax)
	assert.Equal(t, jev.DefaultMaxElapsed, config.MaxElapsed)
}
