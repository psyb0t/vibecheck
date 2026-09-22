//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/psyb0t/vibecheck/tests/testinfra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A transient provider failure is exactly what retries are for: the caller
// should see one successful decision, and the fake should see three calls.
func TestTransientProviderFailuresAreRetriedUntilTheySucceed(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	provider.Reset()
	provider.Enqueue(
		testinfra.OverloadedReply(),
		testinfra.OverloadedReply(),
		defaultReply(),
	)

	result := api.evaluate(t, evaluateBody(
		allowingFacts(), "retry fixture",
	), nil)

	assert.Equal(t, "allow", result["outcome"])
	assert.Equal(t, "completed", result["status"])

	assert.Equal(
		t, 3, provider.CallCount(),
		"two overloads must be retried, and the third call succeeds",
	)

	attempts, ok := result["providerAttempts"]
	require.True(t, ok, "an evaluation records how many attempts it cost")
	assert.InDelta(t, 3.0, attempts, 0,
		"the recorded attempt count must match what the provider saw")
}

// An authentication failure is not transient. Retrying it burns quota and
// delays the error the operator needs to see.
func TestAuthenticationFailuresAreNotRetried(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	provider.Reset()
	provider.SetFallback(testinfra.UnauthorizedReply())

	res := api.do(t, http.MethodPost, "/v1/evaluations",
		evaluateBody(allowingFacts(), "no-retry fixture"), nil)

	assert.GreaterOrEqual(t, res.Status, http.StatusBadRequest)

	assert.Equal(
		t, 1, provider.CallCount(),
		"a 401 is a configuration fault, so it must be attempted once",
	)
}

// Exhausting the retry budget still has to produce an audit row, or a
// caller who saw an error has nothing to reference afterwards.
func TestAnExhaustedRetryBudgetIsStillRecorded(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{
		Env: map[string]string{"VIBECHECK_TYPESAFE_MAX_ATTEMPTS": "2"},
	})
	api := newClient(app)

	provider.Reset()
	provider.SetFallback(testinfra.OverloadedReply())

	res := api.do(t, http.MethodPost, "/v1/evaluations",
		evaluateBody(allowingFacts(), "exhausted budget fixture"), nil)

	assert.GreaterOrEqual(t, res.Status, http.StatusInternalServerError)
	assert.NotEmpty(t, res.ErrorCode(t))

	assert.Equal(
		t, 2, provider.CallCount(),
		"the configured attempt ceiling is honoured exactly",
	)

	listed := api.do(t, http.MethodGet,
		"/v1/evaluations?status=provider_failed", nil, nil)
	require.Equal(t, http.StatusOK, listed.Status)

	items, ok := listed.Map(t)["items"].([]any)
	require.True(t, ok)
	require.NotEmpty(
		t, items, "a provider failure is recorded, not dropped",
	)

	failed, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "provider_failed", failed["status"])
	assert.NotEmpty(
		t, failed["failureCode"],
		"a failed evaluation carries a machine-readable failure code",
	)
}

// A provider reply that is not a valid System One document is an upstream
// fault, which is a 502, never a 500 blamed on Vibecheck.
func TestAnUnusableProviderResponseIsReportedAsABadGateway(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	testCases := []struct {
		name string
		body []byte
	}{
		{"not json", []byte("<html>a proxy error page</html>")},
		{"json but not a response", []byte(`{"unexpected":"shape"}`)},
		{
			"an answer missing its value",
			[]byte(`{"model":"m","answers":{"destructive":` +
				`{"type":"noul"}},"usage":{}}`),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider.Reset()
			provider.SetFallback(testinfra.FakeReply{
				Status: http.StatusOK,
				Body:   testCase.body,
			})

			res := api.do(t, http.MethodPost, "/v1/evaluations",
				evaluateBody(allowingFacts(), "protocol fixture"), nil)

			assert.Equal(
				t, http.StatusBadGateway, res.Status,
				"body was %s", string(res.Body),
			)
			assert.NotEmpty(t, res.ErrorCode(t))
		})
	}
}

// A client that hangs up must not leave the request running. The service
// should notice the cancellation rather than finishing the work anyway.
func TestClientCancellationStopsTheRequest(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})

	provider.Reset()
	provider.SetFallback(testinfra.FakeReply{
		Delay:    30 * time.Second,
		Response: defaultReply().Response,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	body := evaluateBody(allowingFacts(), "cancellation fixture")

	req := buildJSONRequest(t, ctx, app.BaseURL+"/v1/evaluations", body)

	start := time.Now()

	//nolint:bodyclose // the request is cancelled, there is no body
	_, err := testinfra.HTTPClient().Do(req)

	require.Error(t, err, "the cancelled request must not return a response")
	assert.Less(
		t, time.Since(start), 20*time.Second,
		"cancellation must take effect well before the provider replies",
	)

	// The service has to stay healthy after a cancelled request.
	res := newClient(app).do(t, http.MethodGet, "/healthz", nil, nil)
	assert.Equal(t, http.StatusOK, res.Status)
}

// The adapter must authenticate to the provider, and must send the policy's
// declared questions with their criteria intact.
func TestTheProviderReceivesACompleteAuthenticatedRequest(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	provider.Reset()

	api.evaluate(t, evaluateBody(allowingFacts(), "wire fixture"), nil)

	calls := provider.Requests()
	require.Len(t, calls, 1, "one evaluation is one provider call")

	call := calls[0]

	assert.Equal(
		t, "Bearer test-provider-key", call.Authorization,
		"the adapter authenticates with the configured credential",
	)

	assert.Equal(
		t, "jev-latest", call.Body.Model,
		"the policy's declared model alias is what gets requested",
	)

	require.Len(
		t, call.Body.Questions, 3,
		"every declared question is asked in one call",
	)

	assert.Equal(t, "noul", call.Body.Questions[questionDestructive].Type)
	assert.Equal(t, "score", call.Body.Questions[questionBlastRadius].Type)
	assert.Equal(t, "choice", call.Body.Questions[questionActionClass].Type)

	assert.Equal(
		t,
		[]any{
			"Confined to one disposable item",
			"Confined to the current project",
			"Could affect unrelated user work",
			"Could affect the host or external systems",
		},
		call.Body.Questions[questionBlastRadius].Criteria,
	)
	assert.Equal(
		t,
		map[string]any{
			"read":                 "Reads state without changing it",
			"reversible_write":     "Changes state with an ordinary recovery path",
			"destructive_write":    "Deletes or irreversibly replaces state",
			"external_side_effect": "Changes an external system or contacts a person",
			"unknown":              "None of the other classes clearly applies",
		},
		call.Body.Questions[questionActionClass].Criteria,
	)

	assert.Equal(
		t, "wire fixture", call.Body.State,
		"the caller's state reaches the provider unchanged",
	)
}

func TestMissingTypeSafeKeyKeepsLivenessButFailsReadiness(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{
		Env: map[string]string{"VIBECHECK_TYPESAFE_API_KEY": ""},
	})
	api := newClient(app)

	live := api.do(t, http.MethodGet, "/healthz", nil, nil)
	ready := api.do(t, http.MethodGet, "/ready", nil, nil)

	assert.Equal(t, http.StatusOK, live.Status)
	assert.Equal(t, http.StatusServiceUnavailable, ready.Status)
	assert.Zero(t, provider.CallCount())
}
