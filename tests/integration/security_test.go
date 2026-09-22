//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/psyb0t/vibecheck/tests/testinfra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAPIToken = "an-api-token-for-the-suite"

func TestBearerAuthGuardsEveryAPIRouteWhenConfigured(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{APIToken: testAPIToken})

	anonymous := newClient(app)
	authorized := anonymous.withToken(testAPIToken)

	guarded := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/v1/policies", nil},
		{
			http.MethodGet,
			"/v1/policies/" + policyName + "/versions/" + policyVersion,
			nil,
		},
		{http.MethodGet, "/v1/evaluations", nil},
		{
			http.MethodPost, "/v1/evaluations",
			evaluateBody(allowingFacts(), "auth fixture"),
		},
	}

	for _, route := range guarded {
		t.Run("anonymous "+route.method+" "+route.path, func(t *testing.T) {
			res := anonymous.do(t, route.method, route.path, route.body, nil)

			assert.Equal(
				t, http.StatusUnauthorized, res.Status,
				"body was %s", string(res.Body),
			)
			assert.NotEmpty(t, res.ErrorCode(t))
		})
	}

	t.Run("a correct token is accepted", func(t *testing.T) {
		res := authorized.do(t, http.MethodGet, "/v1/policies", nil, nil)
		assert.Equal(t, http.StatusOK, res.Status)
	})

	t.Run("a wrong token is refused", func(t *testing.T) {
		res := anonymous.withToken("the-wrong-token").
			do(t, http.MethodGet, "/v1/policies", nil, nil)
		assert.Equal(t, http.StatusUnauthorized, res.Status)
	})

	t.Run("a token prefix is refused", func(t *testing.T) {
		res := anonymous.withToken(testAPIToken[:len(testAPIToken)-1]).
			do(t, http.MethodGet, "/v1/policies", nil, nil)
		assert.Equal(t, http.StatusUnauthorized, res.Status)
	})

	t.Run("MCP is guarded too", func(t *testing.T) {
		res := anonymous.do(t, http.MethodPost, mcpPath, map[string]any{
			"jsonrpc": jsonRPCVersion, "id": 1, "method": "tools/list",
		}, map[string]string{"Accept": mcpAcceptHeader})

		assert.Equal(
			t, http.StatusUnauthorized, res.Status,
			"the MCP surface must not be an auth bypass",
		)
	})

	t.Run("liveness stays open", func(t *testing.T) {
		res := anonymous.do(t, http.MethodGet, "/healthz", nil, nil)
		assert.Equal(
			t, http.StatusOK, res.Status,
			"a probe that needs a credential cannot report liveness",
		)
	})
}

// Inline policies widen the attack surface from "pick a loaded policy" to
// "supply arbitrary rules", so they must be off unless explicitly enabled.
func TestInlinePoliciesAreRefusedUnlessEnabled(t *testing.T) {
	inline := map[string]any{
		"apiVersion": "vibecheck.psyb0t.dev/v1alpha1",
		"kind":       "DecisionPolicy",
		"metadata": map[string]any{
			"name": "inline-policy", "version": "0.1.0",
		},
		"spec": map[string]any{
			"outcomes":       []string{"allow", "review"},
			"defaultOutcome": "review",
			"questions": map[string]any{
				questionDestructive: map[string]any{
					"type":         "noul",
					"instructions": "Is this risky?",
				},
			},
		},
	}

	body := map[string]any{
		"state":        "inline gating fixture",
		"inlinePolicy": inline,
	}

	t.Run("refused by default", func(t *testing.T) {
		res := newClient(sharedApp).do(
			t, http.MethodPost, "/v1/evaluations", body, nil,
		)

		require.Equal(
			t, http.StatusBadRequest, res.Status,
			"body was %s", string(res.Body),
		)
		assert.NotEmpty(t, res.ErrorCode(t))
	})

	t.Run("accepted when the deployment enables them", func(t *testing.T) {
		app, provider := startApp(
			t, testinfra.AppConfig{AllowInlinePolicies: true},
		)

		provider.SetFallback(testinfra.NoulReply(
			[]string{questionDestructive}, 0.9,
		))

		res := newClient(app).do(
			t, http.MethodPost, "/v1/evaluations", body, nil,
		)

		require.Equal(
			t, http.StatusCreated, res.Status, string(res.Body),
		)

		result := res.Map(t)
		assert.Equal(t, "inline", result["policyKind"])
		assert.Equal(t, "review", result["outcome"],
			"no rule matched, so the default outcome stands")
	})
}

// Supplying both a ref and an inline document is ambiguous. Guessing which
// one the caller meant is how the wrong rules get applied.
func TestSupplyingBothPolicyRefAndInlinePolicyIsRefused(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{AllowInlinePolicies: true})

	res := newClient(app).do(t, http.MethodPost, "/v1/evaluations",
		map[string]any{
			"state": "ambiguous selection",
			"facts": allowingFacts(),
			"policyRef": map[string]any{
				"name": policyName, "version": policyVersion,
			},
			"inlinePolicy": map[string]any{
				"apiVersion": "vibecheck.psyb0t.dev/v1alpha1",
				"kind":       "DecisionPolicy",
				"metadata": map[string]any{
					"name": "x", "version": "0.1.0",
				},
				"spec": map[string]any{
					"outcomes":       []string{"allow"},
					"defaultOutcome": "allow",
					"questions": map[string]any{
						"q": map[string]any{
							"type": "noul", "instructions": "?",
						},
					},
				},
			},
		}, nil)

	assert.Equal(t, http.StatusBadRequest, res.Status, string(res.Body))
}

func TestOversizedRequestsAreRefusedBeforeDecoding(t *testing.T) {
	const smallLimit = 4096

	app, _ := startApp(t, testinfra.AppConfig{
		Env: map[string]string{
			"VIBECHECK_MAX_REQUEST_BYTES": "4096",
			"VIBECHECK_MAX_STATE_BYTES":   "2048",
		},
	})

	api := newClient(app)

	oversized := strings.Repeat("A", smallLimit*4)

	res := api.do(t, http.MethodPost, "/v1/evaluations",
		evaluateBody(allowingFacts(), oversized), nil)

	assert.GreaterOrEqual(t, res.Status, http.StatusBadRequest)
	assert.Less(
		t, res.Status, http.StatusInternalServerError,
		"an oversized body is refused, not a server error: %s",
		string(res.Body),
	)
}

// Nothing the service logs may contain the provider credential, the API
// token, or the caller's raw state.
func TestSecretsAndRequestBodiesNeverReachTheLogs(t *testing.T) {
	const distinctiveState = "SUPER-DISTINCTIVE-CALLER-STATE-8f2a"

	app, provider := startApp(t, testinfra.AppConfig{
		APIToken: testAPIToken,
	})

	api := newClient(app).withToken(testAPIToken)

	api.evaluate(t, evaluateBody(allowingFacts(), distinctiveState), nil)

	// Force an error path too, since that is where a lazy implementation
	// dumps the upstream body.
	provider.Enqueue(testinfra.UnauthorizedReply())
	api.do(t, http.MethodPost, "/v1/evaluations",
		evaluateBody(allowingFacts(), distinctiveState+"-on-failure"), nil)

	logs, err := app.Logs(context.Background())
	require.NoError(t, err, "read the container logs")
	require.NotEmpty(t, logs, "the service logs something")

	forbidden := map[string]string{
		"the configured API token":   testAPIToken,
		"the provider credential":    "test-provider-key",
		"the caller's raw state":     distinctiveState,
		"the provider error message": testinfra.ProviderErrorMarker,
	}

	for what, secret := range forbidden {
		assert.NotContains(
			t, logs, secret,
			"%s must never appear in the logs", what,
		)
	}
}

// A provider error body can echo the caller's own state back. It must never
// be forwarded to an API caller.
func TestProviderErrorMessagesAreNeverForwardedToCallers(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	provider.SetFallback(testinfra.UnauthorizedReply())

	res := api.do(t, http.MethodPost, "/v1/evaluations",
		evaluateBody(allowingFacts(), "leak probe"), nil)

	assert.GreaterOrEqual(t, res.Status, http.StatusBadRequest)
	assert.NotContains(
		t, string(res.Body), testinfra.ProviderErrorMarker,
		"the provider's own message must not reach the caller",
	)
	assert.NotEmpty(t, res.ErrorCode(t))
}

// Retention is off by default, and turning it on without a key must be
// refused rather than silently storing plaintext.
func TestRetainedInputIsEncryptedAtRest(t *testing.T) {
	const secretState = "RETAINED-PLAINTEXT-MARKER-4c19"

	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err, "generate a data key")

	app, _ := startApp(t, testinfra.AppConfig{
		StoreInputs: true,
		DataKey:     base64.StdEncoding.EncodeToString(key),
	})

	api := newClient(app)
	created := api.evaluate(
		t, evaluateBody(allowingFacts(), secretState), nil,
	)

	id, ok := created["id"].(string)
	require.True(t, ok)

	ctx := context.Background()

	exit, reader, err := app.Container.Exec(ctx, []string{
		"grep", "-c", secretState, "/data/vibecheck.sqlite",
	})
	require.NoError(t, err, "search the database file")

	_ = reader //nolint:wsl_v5 // the exit code carries the answer

	assert.NotEqual(
		t, 0, exit,
		"the retained input must not be readable as plaintext on disk",
	)

	res := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
	require.Equal(t, http.StatusOK, res.Status)
	assert.NotContains(
		t, string(res.Body), secretState,
		"retained input is audit material, not part of the API response",
	)
}

func TestSecurityHeadersAreSetOnEveryResponse(t *testing.T) {
	api := newClient(sharedApp)

	res := api.do(t, http.MethodGet, "/v1/policies", nil, nil)

	expected := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'",
	}

	for header, want := range expected {
		assert.Equal(t, want, res.Header.Get(header), "header %s", header)
	}
}

// The metrics listener carries traffic shape and error rates. It must not be
// reachable on the public port.
func TestMetricsAreOnlyOnTheInternalListener(t *testing.T) {
	api := newClient(sharedApp)

	res := api.do(t, http.MethodGet, "/metrics", nil, nil)
	assert.Equal(
		t, http.StatusNotFound, res.Status,
		"the public listener must not serve the metrics endpoint",
	)

	internal := testinfra.HTTPClient()

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, sharedApp.MetricsURL, nil,
	)
	require.NoError(t, err)

	scrape, err := internal.Do(req)
	require.NoError(t, err, "scrape the internal listener")

	defer func() { _ = scrape.Body.Close() }() //nolint:errcheck // read side

	assert.Equal(t, http.StatusOK, scrape.StatusCode)
}
