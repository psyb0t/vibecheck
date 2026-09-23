//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/psyb0t/vibecheck/tests/testinfra"
	"github.com/stretchr/testify/require"
)

// The policy the suite evaluates against, and the identifiers a test uses to
// name it in a request.
const (
	policyFileName = "firewall.yaml"
	policyName     = "agent-action-firewall"
	policyVersion  = "1.0.0"
)

// Question IDs declared by that policy.
const (
	questionDestructive      = "destructive"
	questionBlastRadius      = "blastRadius"
	questionActionClass      = "actionClass"
	choiceRead               = "read"
	choiceReversibleWrit     = "reversible_write"
	choiceDestructiveWrite   = "destructive_write"
	choiceExternalSideEffect = "external_side_effect"
	choiceUnknown            = "unknown"
)

// Header names the API contract defines.
const (
	headerRequestID      = "X-Request-Id"
	headerIdempotencyKey = "Idempotency-Key"
	headerAuthorization  = "Authorization"
	headerContentType    = "Content-Type"
)

const contentTypeJSON = "application/json"

// firewallPolicy is the reference policy, inlined so the suite does not
// depend on a file outside its own package.
func firewallPolicy() []byte {
	return []byte(`apiVersion: vibecheck.psyb0t.dev/v1alpha1
kind: DecisionPolicy
metadata:
  name: agent-action-firewall
  version: 1.0.0
  description: Judge a proposed coding-agent action.
spec:
  model: jev-latest
  outcomes: [allow, review, block]
  defaultOutcome: review
  input:
    requiredFacts:
      action.kind: string
      action.destructive: boolean
      target.ownedBySession: boolean
      authorization.explicit: boolean
    optionalFacts:
      target.path: string
  preRules:
    - id: block-unowned-destructive-target
      description: A model cannot override the ownership floor.
      when:
        all:
          - left: {fact: action.destructive}
            op: eq
            right: true
          - left: {fact: target.ownedBySession}
            op: eq
            right: false
      outcome: block
  questions:
    destructive:
      type: noul
      instructions: Is the proposed action destructive or hard to reverse?
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
      instructions: Which class best describes the proposed action?
      criteria:
        read: Reads state without changing it
        reversible_write: Changes state with an ordinary recovery path
        destructive_write: Deletes or irreversibly replaces state
        external_side_effect: Changes an external system or contacts a person
        unknown: None of the other classes clearly applies
  decisionRules:
    - id: review-ambiguous-action
      description: An unclear or low-confidence classification goes to a human.
      when:
        any:
          - left: {answer: actionClass, field: choice}
            op: eq
            right: unknown
          - left: {answer: actionClass, field: confidence}
            op: lt
            right: 0.65
      outcome: review
    - id: block-high-risk-without-authorization
      description: Wide blast radius needs an explicit go-ahead.
      when:
        all:
          - left: {answer: blastRadius, field: score}
            op: gte
            right: 2.5
          - left: {fact: authorization.explicit}
            op: eq
            right: false
      outcome: block
    - id: review-meaningful-destructive-probability
      description: A meaningful chance of destructive work still needs review.
      when:
        left: {answer: actionClass, field: probability, probabilityKey: destructive_write}
        op: gte
        right: 0.2
      outcome: review
    - id: allow-owned-low-risk-action
      description: Owned, non-destructive, and narrow enough to proceed.
      when:
        all:
          - left: {fact: target.ownedBySession}
            op: eq
            right: true
          - left: {answer: destructive, field: noul}
            op: lt
            right: 0.35
          - left: {answer: blastRadius, field: score}
            op: lt
            right: 1.5
      outcome: allow
`)
}

// defaultReply answers every question so the allow rule fires.
//
// The distributions are complete on purpose. The policy contract requires a
// probability for every declared option and every score level, plus a legend
// covering each level, so a fake that sends a partial distribution would be
// rejected as a provider protocol error exactly like a real one would.
func defaultReply() testinfra.FakeReply {
	const confidence = 0.92

	actionClass := testinfra.ChoiceAnswer(
		choiceReversibleWrit,
		map[string]float64{
			choiceReversibleWrit:     0.92,
			choiceRead:               0.04,
			choiceDestructiveWrite:   0.02,
			choiceExternalSideEffect: 0.01,
			choiceUnknown:            0.01,
		},
	)
	actionClass.Confidence = confidencePtr(confidence)

	blastRadius := testinfra.ScoreAnswer(0.5)
	blastRadius.Confidence = confidencePtr(confidence)
	blastRadius.Probabilities = map[string]float64{
		"0": 0.55, "1": 0.40, "2": 0.04, "3": 0.01,
	}
	blastRadius.Legend = blastRadiusLegend()

	return testinfra.AnswerReply(map[string]testinfra.FakeAnswer{
		questionDestructive: testinfra.NoulAnswer(0.1),
		questionBlastRadius: blastRadius,
		questionActionClass: actionClass,
	})
}

// uncertainDestructiveReply selects read while assigning enough probability
// to destructive work for the policy to require review.
func uncertainDestructiveReply() testinfra.FakeReply {
	const (
		readProbability               = 0.55
		reversibleWriteProbability    = 0.10
		destructiveWriteProbability   = 0.25
		externalSideEffectProbability = 0.05
		unknownProbability            = 0.05
	)

	reply := defaultReply()
	actionClass := reply.Response.Answers[questionActionClass]
	actionClass.Choice = choiceRead
	actionClass.Probabilities = map[string]float64{
		choiceRead:               readProbability,
		choiceReversibleWrit:     reversibleWriteProbability,
		choiceDestructiveWrite:   destructiveWriteProbability,
		choiceExternalSideEffect: externalSideEffectProbability,
		choiceUnknown:            unknownProbability,
	}
	reply.Response.Answers[questionActionClass] = actionClass

	return reply
}

// blastRadiusLegend names each score level, keyed by its index, which is the
// form the policy contract requires.
func blastRadiusLegend() map[string]any {
	return map[string]any{
		"0": "Confined to one disposable item",
		"1": "Confined to the current project",
		"2": "Could affect unrelated user work",
		"3": "Could affect the host or external systems",
	}
}

func confidencePtr(value float64) *float64 {
	return &value
}

// allowingFacts satisfy the policy's required facts and avoid the pre-rule.
func allowingFacts() map[string]any {
	return map[string]any{
		"action.kind":            "edit_file",
		"action.destructive":     false,
		"target.ownedBySession":  true,
		"authorization.explicit": true,
	}
}

// blockingFacts trip the deterministic pre-rule, which must decide without
// the provider being consulted at all.
func blockingFacts() map[string]any {
	return map[string]any{
		"action.kind":            "delete_file",
		"action.destructive":     true,
		"target.ownedBySession":  false,
		"authorization.explicit": true,
	}
}

// evaluateBody builds a createEvaluation request body.
func evaluateBody(facts map[string]any, state any) map[string]any {
	return map[string]any{
		"policyRef": map[string]any{
			"name":    policyName,
			"version": policyVersion,
		},
		"state": state,
		"facts": facts,
	}
}

// response is a decoded HTTP reply a test asserts on.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into target, failing the test if it is not JSON.
func (r response) JSON(t *testing.T, target any) {
	t.Helper()

	require.NoError(
		t, json.Unmarshal(r.Body, target),
		"response body must be JSON: %s", string(r.Body),
	)
}

// Map decodes the body into a generic map.
func (r response) Map(t *testing.T) map[string]any {
	t.Helper()

	out := map[string]any{}
	r.JSON(t, &out)

	return out
}

// ErrorCode reads the stable machine-readable code from an error envelope.
func (r response) ErrorCode(t *testing.T) string {
	t.Helper()

	body := r.Map(t)

	code, ok := body["code"].(string)
	require.True(
		t, ok, "error responses carry a string code: %s", string(r.Body),
	)

	return code
}

// client issues requests against one app.
type client struct {
	baseURL string
	token   string
	http    *http.Client
}

func newClient(app *testinfra.App) *client {
	return &client{baseURL: app.BaseURL, http: testinfra.HTTPClient()}
}

func (c *client) withToken(token string) *client {
	return &client{baseURL: c.baseURL, token: token, http: c.http}
}

// do issues one request and reads the whole reply.
func (c *client) do(
	t *testing.T,
	method, path string,
	body any,
	headers map[string]string,
) response {
	t.Helper()

	var reader io.Reader

	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err, "encode request body")

		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(
		context.Background(), method, c.baseURL+path, reader,
	)
	require.NoError(t, err, "build request")

	if body != nil {
		req.Header.Set(headerContentType, contentTypeJSON)
	}

	if c.token != "" {
		req.Header.Set(headerAuthorization, "Bearer "+c.token)
	}

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	return c.send(t, req)
}

// doRaw issues a request with a caller-supplied body, so a test can send
// bytes that are deliberately not valid JSON.
func (c *client) doRaw(
	t *testing.T,
	method, path string,
	body []byte,
	headers map[string]string,
) response {
	t.Helper()

	req, err := http.NewRequestWithContext(
		context.Background(), method, c.baseURL+path, bytes.NewReader(body),
	)
	require.NoError(t, err, "build request")

	req.Header.Set(headerContentType, contentTypeJSON)

	if c.token != "" {
		req.Header.Set(headerAuthorization, "Bearer "+c.token)
	}

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	return c.send(t, req)
}

func (c *client) send(t *testing.T, req *http.Request) response {
	t.Helper()

	res, err := c.http.Do(req)
	require.NoError(t, err, "issue %s %s", req.Method, req.URL.Path)

	defer func() { _ = res.Body.Close() }() //nolint:errcheck // read side

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err, "read response body")

	return response{Status: res.StatusCode, Header: res.Header, Body: body}
}

// evaluate posts one evaluation and requires it to succeed.
func (c *client) evaluate(
	t *testing.T,
	body any,
	headers map[string]string,
) map[string]any {
	t.Helper()

	res := c.do(t, http.MethodPost, "/v1/evaluations", body, headers)
	require.Equal(
		t, http.StatusCreated, res.Status,
		"evaluation should succeed: %s", string(res.Body),
	)

	return res.Map(t)
}

// startApp brings up a dedicated app plus its own fake provider.
func startApp(
	t *testing.T,
	config testinfra.AppConfig,
) (*testinfra.App, *testinfra.FakeProvider) {
	t.Helper()

	ctx := context.Background()

	port, err := testinfra.FreePort()
	require.NoError(t, err, "reserve a provider port")

	provider, err := testinfra.StartFakeProvider(port, defaultReply())
	require.NoError(t, err, "start the fake provider")

	t.Cleanup(func() { _ = provider.Close() }) //nolint:errcheck // cleanup

	if config.Policies == nil {
		config.Policies = map[string][]byte{policyFileName: firewallPolicy()}
	}

	config.ProviderBaseURL = provider.URL()
	config.HostPorts = append(config.HostPorts, port)

	app, err := testinfra.StartApp(ctx, config, nil)
	require.NoError(t, err, "start the app container")

	t.Cleanup(func() { app.Terminate(context.Background()) })

	return app, provider
}
