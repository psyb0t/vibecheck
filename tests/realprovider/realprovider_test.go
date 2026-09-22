//go:build integration && realprovider

// Package realprovider holds the opt-in suite that calls the live TypeSafe
// Jev API.
//
// It is never part of `make test` and never runs in CI, because every run
// bills a real account. Run it with `make test-real` after putting a real
// credential in the gitignored .env.real. See docs/testing.md.
//
// What it proves that the fake cannot: the request Vibecheck builds is one
// the real System One endpoint accepts, and the response the adapter decodes
// is the shape the real service still sends. The fake is faithful to the
// contract as documented; this suite checks the documented contract is still
// the real one.
package realprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/psyb0t/vibecheck/tests/testinfra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const apiKeyEnvVar = "VIBECHECK_TYPESAFE_API_KEY"

const (
	policyFileName = "live.yaml"
	livePolicyName = "live-provider-check"
	livePolicyVer  = "1.0.0"
)

// liveCallTimeout is generous: a real model call is slower and more variable
// than a local fake, and a flaky timeout here would look like a contract
// break when it is only latency.
const liveCallTimeout = 2 * time.Minute

// livePolicy asks one question of each type, so a single billed call
// exercises every answer shape the adapter decodes.
func livePolicy() []byte {
	return []byte(`apiVersion: vibecheck.psyb0t.dev/v1alpha1
kind: DecisionPolicy
metadata:
  name: live-provider-check
  version: 1.0.0
  description: Exercise every answer shape against the live provider.
spec:
  model: jev-latest
  outcomes: [allow, review]
  defaultOutcome: review
  questions:
    destructive:
      type: noul
      instructions: Is the described action destructive or hard to reverse?
    blastRadius:
      type: score
      instructions: How broad is the potential impact of this action?
      criteria:
        - Confined to one disposable item
        - Confined to the current project
        - Could affect unrelated user work
        - Could affect the host or external systems
    actionClass:
      type: choice
      instructions: Which class best describes the described action?
      criteria:
        read: Reads state without changing it
        write: Changes state with an ordinary recovery path
        unknown: None of the other classes clearly applies
  decisionRules:
    - id: allow-clear-read
      description: A confidently classified read is allowed.
      when:
        all:
          - left: {answer: actionClass, field: choice}
            op: eq
            right: read
          - left: {answer: destructive, field: noul}
            op: lt
            right: 0.35
      outcome: allow
`)
}

// The live provider is slower and less predictable than the fake, so this
// asserts on contract shape rather than on a specific decision. A real model
// is allowed to disagree about how risky a sentence is. It is not allowed to
// answer in a shape the adapter cannot read.
func TestLiveProviderAnswersTheDocumentedContract(t *testing.T) {
	apiKey := os.Getenv(apiKeyEnvVar)
	if apiKey == "" {
		t.Skipf(
			"%s is not set; this suite is opt-in, see docs/testing.md",
			apiKeyEnvVar,
		)
	}

	ctx := context.Background()

	app, err := testinfra.StartApp(ctx, testinfra.AppConfig{
		Policies: map[string][]byte{policyFileName: livePolicy()},
		Env: map[string]string{
			apiKeyEnvVar:                 apiKey,
			"VIBECHECK_TYPESAFE_TIMEOUT": "60s",
		},
	}, nil)
	require.NoError(t, err, "start the app against the live provider")

	t.Cleanup(func() { app.Terminate(context.Background()) })

	result := postEvaluation(t, app, map[string]any{
		"policyRef": map[string]any{
			"name": livePolicyName, "version": livePolicyVer,
		},
		"state": "Open README.md and print its first ten lines.",
	})

	assert.Equal(
		t, "succeeded", result["status"],
		"the live provider answered within the contract",
	)

	assert.NotEmpty(
		t, result["resolvedModel"],
		"the live provider reports which model version answered",
	)

	answers, ok := result["answers"].(map[string]any)
	require.True(t, ok, "the response carries the answer set")

	for _, id := range []string{
		"destructive", "blastRadius", "actionClass",
	} {
		assert.Contains(
			t, answers, id,
			"the live provider answered every declared question",
		)
	}

	usage, ok := result["usage"].(map[string]any)
	require.True(t, ok, "the live provider reports usage")

	inputTokens, ok := usage["inputTokens"].(float64)
	require.True(t, ok, "input tokens are reported as a number")
	assert.Positive(t, inputTokens, "a real call bills real input tokens")

	assert.Contains(
		t, []any{"allow", "review"}, result["outcome"],
		"the outcome is one the policy declares",
	)
}

// postEvaluation submits one evaluation and requires it to succeed.
func postEvaluation(
	t *testing.T,
	app *testinfra.App,
	body map[string]any,
) map[string]any {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err, "encode the request body")

	ctx, cancel := context.WithTimeout(context.Background(), liveCallTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		app.BaseURL+"/v1/evaluations",
		bytes.NewReader(encoded),
	)
	require.NoError(t, err, "build the request")

	req.Header.Set("Content-Type", "application/json")

	res, err := testinfra.HTTPClient().Do(req)
	require.NoError(t, err, "call the live-backed service")

	defer func() { _ = res.Body.Close() }() //nolint:errcheck // read side

	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err, "read the response body")

	require.Equal(
		t, http.StatusCreated, res.StatusCode,
		"the live evaluation should succeed: %s", string(raw),
	)

	out := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &out), "decode the response")

	return out
}
