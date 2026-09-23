//go:build integration

package integration

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/psyb0t/vibecheck/tests/testinfra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationRecordsTheProviderAnswersAndTheChosenRule(t *testing.T) {
	api := newClient(sharedApp)

	before := sharedProvider.CallCount()

	result := api.evaluate(
		t, evaluateBody(allowingFacts(), "rewrite one owned file"), nil,
	)

	assert.Equal(t, "allow", result["outcome"])
	assert.Equal(t, "decision_rule", result["executionPath"])
	assert.Equal(t, "allow-owned-low-risk-action", result["matchedRuleId"])
	assert.Equal(t, "completed", result["status"])

	assert.Equal(
		t, testinfra.DefaultFakeModel, result["resolvedModel"],
		"the model that answered is recorded, not the alias requested",
	)

	usage, ok := result["usage"].(map[string]any)
	require.True(t, ok, "an evaluation reports token usage")
	assert.InDelta(t, float64(testinfra.FakeInputTokens),
		usage["inputTokens"], 0)
	assert.InDelta(t, float64(testinfra.FakeOutputTokens),
		usage["outputTokens"], 0)

	answers, ok := result["answers"].(map[string]any)
	require.True(t, ok, "every provider answer is returned")
	assert.Contains(t, answers, questionDestructive)
	assert.Contains(t, answers, questionBlastRadius)
	assert.Contains(t, answers, questionActionClass)

	assert.Greater(
		t, sharedProvider.CallCount(), before,
		"a decision-rule path must actually call the provider",
	)

	id, ok := result["id"].(string)
	require.True(t, ok, "an evaluation has a stable id")
	_, err := uuid.Parse(id)
	require.NoError(t, err, "the evaluation id is a UUID")

	scrape := scrapeMetrics(t, sharedApp)
	assert.Contains(t, scrape, "vibecheck_provider_attempts_per_call")
	assert.Contains(t, scrape, "vibecheck_model_input_tokens_total")
	assert.Contains(t, scrape, "vibecheck_model_output_tokens_total")
	assert.Contains(t, scrape, "vibecheck_evaluation_total")
	assert.Contains(t, scrape, "vibecheck_db_operations_total")
}

func TestEvaluationRoutesAnUnselectedDestructiveOptionToReview(t *testing.T) {
	app, provider := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	provider.Reset()
	provider.SetFallback(uncertainDestructiveReply())

	created := api.evaluate(
		t, evaluateBody(allowingFacts(), "classify an owned write"), nil,
	)
	assert.Equal(t, "review", created["outcome"])
	assert.Equal(t, "decision_rule", created["executionPath"])
	assert.Equal(
		t,
		"review-meaningful-destructive-probability",
		created["matchedRuleId"],
	)
	assert.Equal(t, 1, provider.CallCount())

	id, ok := created["id"].(string)
	require.True(t, ok, "the evaluation response includes its audit ID")

	fetchedResponse := api.do(
		t, http.MethodGet, "/v1/evaluations/"+id, nil, nil,
	)
	require.Equal(t, http.StatusOK, fetchedResponse.Status)

	fetched := fetchedResponse.Map(t)
	assert.Equal(t, created["outcome"], fetched["outcome"])
	assert.Equal(t, created["matchedRuleId"], fetched["matchedRuleId"])
}

// A pre-rule is the deterministic floor. If it decides, the provider must
// never be consulted: that is what stops a model from overriding ownership.
func TestPreRuleDecidesWithoutCallingTheProvider(t *testing.T) {
	api := newClient(sharedApp)

	before := sharedProvider.CallCount()

	result := api.evaluate(
		t, evaluateBody(blockingFacts(), "delete an unowned path"), nil,
	)

	assert.Equal(t, "block", result["outcome"])
	assert.Equal(t, "pre_rule", result["executionPath"])
	assert.Equal(
		t, "block-unowned-destructive-target", result["matchedRuleId"],
	)

	assert.Equal(
		t, before, sharedProvider.CallCount(),
		"a pre-rule decision must not reach the provider",
	)
}

func TestEvaluationRejectsBadRequests(t *testing.T) {
	api := newClient(sharedApp)

	testCases := []struct {
		name       string
		body       any
		wantStatus int
	}{
		{
			name: "unknown policy",
			body: map[string]any{
				"policyRef": map[string]any{
					"name": "no-such-policy", "version": "1.0.0",
				},
				"state": "anything",
				"facts": allowingFacts(),
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "known policy at an unknown version",
			body: map[string]any{
				"policyRef": map[string]any{
					"name": policyName, "version": "9.9.9",
				},
				"state": "anything",
				"facts": allowingFacts(),
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "missing a required fact",
			body: evaluateBody(map[string]any{
				"action.kind":        "edit_file",
				"action.destructive": false,
			}, "state"),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "a required fact of the wrong type",
			body: evaluateBody(map[string]any{
				"action.kind":            "edit_file",
				"action.destructive":     "not-a-boolean",
				"target.ownedBySession":  true,
				"authorization.explicit": true,
			}, "state"),
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name: "no policy selected at all",
			body: map[string]any{
				"state": "anything",
				"facts": allowingFacts(),
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := api.do(
				t, http.MethodPost, "/v1/evaluations", testCase.body, nil,
			)

			require.Equal(
				t, testCase.wantStatus, res.Status,
				"body was %s", string(res.Body),
			)
			assert.NotEmpty(
				t, res.ErrorCode(t),
				"every failure carries a stable machine-readable code",
			)
		})
	}
}

func TestMalformedBodyIsRejectedWithTheStandardEnvelope(t *testing.T) {
	api := newClient(sharedApp)

	testCases := []struct {
		name string
		body []byte
	}{
		{"not json at all", []byte("this is not json")},
		{"truncated json", []byte(`{"state": "x", `)},
		{"a json array where an object is required", []byte(`[1,2,3]`)},
		{"empty body", []byte{}},
		{
			"unknown top-level field",
			[]byte(`{"policyRef":{"name":"agent-action-firewall","version":"1.0.0"},"state":"x","facts":{},"typo":true}`), //nolint:lll // byte-exact malformed protocol fixture
		},
		{
			"concatenated json values",
			[]byte(`{"state":"x"} {"state":"y"}`),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := api.doRaw(
				t, http.MethodPost, "/v1/evaluations", testCase.body, nil,
			)

			assert.GreaterOrEqual(t, res.Status, http.StatusBadRequest)
			assert.Less(t, res.Status, http.StatusInternalServerError,
				"malformed input is the caller's fault, not a 5xx")
			assert.NotEmpty(t, res.ErrorCode(t))
		})
	}
}

func TestGetEvaluationReturnsTheRecordedDecision(t *testing.T) {
	api := newClient(sharedApp)

	created := api.evaluate(
		t, evaluateBody(allowingFacts(), "fetch me back"), nil,
	)
	id, ok := created["id"].(string)
	require.True(t, ok)

	res := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))

	fetched := res.Map(t)
	assert.Equal(t, created["id"], fetched["id"])
	assert.Equal(t, created["outcome"], fetched["outcome"])
	assert.Equal(t, created["matchedRuleId"], fetched["matchedRuleId"])
}

func TestGetEvaluationRejectsUnknownAndMalformedIDs(t *testing.T) {
	api := newClient(sharedApp)

	t.Run("unknown but well formed", func(t *testing.T) {
		res := api.do(
			t, http.MethodGet,
			"/v1/evaluations/"+uuid.NewString(), nil, nil,
		)
		assert.Equal(t, http.StatusNotFound, res.Status)
		assert.NotEmpty(t, res.ErrorCode(t))
	})

	t.Run("not a uuid", func(t *testing.T) {
		res := api.do(
			t, http.MethodGet, "/v1/evaluations/not-a-uuid", nil, nil,
		)
		assert.Equal(t, http.StatusBadRequest, res.Status)
	})
}

// Pagination has to work across more than one page, with no duplicates and
// no gaps, or an auditor reading the log silently misses rows.
func TestListEvaluationsPagesWithoutGapsOrDuplicates(t *testing.T) {
	app, _ := startApp(t, testinfra.AppConfig{})
	api := newClient(app)

	const total = 7

	created := make(map[string]bool, total)

	for i := range total {
		result := api.evaluate(t, evaluateBody(
			allowingFacts(), "page fixture "+strconv.Itoa(i),
		), nil)

		id, ok := result["id"].(string)
		require.True(t, ok)
		created[id] = true
	}

	const pageSize = 3

	seen := map[string]bool{}
	pages := 0

	for offset := 0; ; offset += pageSize {
		res := api.do(t, http.MethodGet,
			"/v1/evaluations?limit="+strconv.Itoa(pageSize)+
				"&offset="+strconv.Itoa(offset), nil, nil)
		require.Equal(t, http.StatusOK, res.Status, string(res.Body))

		page := res.Map(t)
		pages++

		items, ok := page["items"].([]any)
		require.True(t, ok, "a page carries an items array")

		for _, entry := range items {
			item, ok := entry.(map[string]any)
			require.True(t, ok)

			id, ok := item["id"].(string)
			require.True(t, ok)

			require.False(t, seen[id], "id %s appeared on two pages", id)
			seen[id] = true
		}

		hasMore, ok := page["hasMore"].(bool)
		require.True(t, ok, "a page reports whether more rows follow")

		if !hasMore {
			break
		}

		require.Less(t, pages, total, "pagination must terminate")
	}

	assert.Greater(t, pages, 1, "the fixture must span several pages")
	assert.Len(t, seen, total, "every created evaluation appears exactly once")

	for id := range created {
		assert.True(t, seen[id], "evaluation %s was never listed", id)
	}
}

func TestListEvaluationsRejectsOutOfRangePaging(t *testing.T) {
	api := newClient(sharedApp)

	testCases := []struct {
		name  string
		query string
	}{
		{"limit below one", "?limit=0"},
		{"negative limit", "?limit=-1"},
		{"limit above the documented maximum", "?limit=100000"},
		{"negative offset", "?offset=-5"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := api.do(
				t, http.MethodGet, "/v1/evaluations"+testCase.query, nil, nil,
			)

			assert.GreaterOrEqual(t, res.Status, http.StatusBadRequest)
			assert.Less(t, res.Status, http.StatusInternalServerError)
		})
	}
}

func TestListEvaluationsFiltersByPolicyAndOutcome(t *testing.T) {
	// Both assertions are about what a filter returns, not about how many
	// rows exist, so they hold on the shared app.
	api := newClient(sharedApp)

	api.evaluate(t, evaluateBody(allowingFacts(), "an allowed action"), nil)
	api.evaluate(t, evaluateBody(blockingFacts(), "a blocked action"), nil)

	t.Run("by outcome", func(t *testing.T) {
		res := api.do(
			t, http.MethodGet, "/v1/evaluations?outcome=block", nil, nil,
		)
		require.Equal(t, http.StatusOK, res.Status)

		items, ok := res.Map(t)["items"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, items, "the blocked evaluation must be findable")

		for _, entry := range items {
			item, ok := entry.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "block", item["outcome"])
		}
	})

	t.Run("by a policy that recorded nothing", func(t *testing.T) {
		res := api.do(t, http.MethodGet,
			"/v1/evaluations?policyName=not-a-policy", nil, nil)
		require.Equal(t, http.StatusOK, res.Status)

		items, ok := res.Map(t)["items"].([]any)
		require.True(t, ok)
		assert.Empty(t, items, "an unmatched filter returns an empty page")
	})
}

func TestFeedbackRecordsTheExpectedOutcomeWithoutMutatingTheDecision(
	t *testing.T,
) {
	api := newClient(sharedApp)

	created := api.evaluate(
		t, evaluateBody(allowingFacts(), "feedback fixture"), nil,
	)
	id, ok := created["id"].(string)
	require.True(t, ok)

	res := api.do(t, http.MethodPost,
		"/v1/evaluations/"+id+"/feedback",
		map[string]any{
			"expectedOutcome": "review",
			"note":            "a human would have escalated this",
		}, nil)
	require.Equal(t, http.StatusCreated, res.Status, string(res.Body))

	feedback := res.Map(t)
	assert.Equal(t, "review", feedback["expectedOutcome"])
	assert.Equal(t, id, feedback["evaluationId"])

	after := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
	require.Equal(t, http.StatusOK, after.Status)
	assert.Equal(
		t, created["outcome"], after.Map(t)["outcome"],
		"feedback is calibration data and must never rewrite the decision",
	)
}

func TestFeedbackRejectsBadInput(t *testing.T) {
	api := newClient(sharedApp)

	created := api.evaluate(
		t, evaluateBody(allowingFacts(), "feedback validation fixture"), nil,
	)
	id, ok := created["id"].(string)
	require.True(t, ok)

	testCases := []struct {
		name       string
		path       string
		body       any
		wantStatus int
	}{
		{
			name:       "an outcome the policy does not declare",
			path:       "/v1/evaluations/" + id + "/feedback",
			body:       map[string]any{"expectedOutcome": "teleport"},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "an unknown evaluation",
			path:       "/v1/evaluations/" + uuid.NewString() + "/feedback",
			body:       map[string]any{"expectedOutcome": "review"},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			res := api.do(
				t, http.MethodPost, testCase.path, testCase.body, nil,
			)
			require.Equal(
				t, testCase.wantStatus, res.Status, string(res.Body),
			)
			assert.NotEmpty(t, res.ErrorCode(t))
		})
	}
}

func TestPolicyEndpointsServeTheLoadedSet(t *testing.T) {
	api := newClient(sharedApp)

	t.Run("list", func(t *testing.T) {
		res := api.do(t, http.MethodGet, "/v1/policies", nil, nil)
		require.Equal(t, http.StatusOK, res.Status)

		items, ok := res.Map(t)["items"].([]any)
		require.True(t, ok)
		require.Len(t, items, 1, "one policy was mounted")

		item, ok := items[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, policyName, item["name"])
		assert.Equal(t, policyVersion, item["version"])
		assert.NotEmpty(t, item["hash"], "a policy is identified by its hash")
	})

	t.Run("get", func(t *testing.T) {
		res := api.do(t, http.MethodGet,
			"/v1/policies/"+policyName+"/versions/"+policyVersion, nil, nil)
		require.Equal(t, http.StatusOK, res.Status)

		policy := res.Map(t)
		assert.Equal(t, policyName, policy["name"])
		assert.Equal(t, "review", policy["defaultOutcome"])
	})

	t.Run("get an unknown policy", func(t *testing.T) {
		res := api.do(t, http.MethodGet,
			"/v1/policies/nope/versions/1.0.0", nil, nil)
		assert.Equal(t, http.StatusNotFound, res.Status)
		assert.NotEmpty(t, res.ErrorCode(t))
	})
}

// Validation compiles a document and reports on it. It must never install
// the document, or an unauthenticated caller could add rules to a running
// deployment.
func TestValidatePolicyReportsWithoutInstalling(t *testing.T) {
	// The shared app already has exactly one policy loaded, which is the
	// state this asserts is unchanged. A dedicated container would add a
	// build and a boot to prove the same thing.
	api := newClient(sharedApp)

	candidate := map[string]any{
		"apiVersion": "vibecheck.psyb0t.dev/v1alpha1",
		"kind":       "DecisionPolicy",
		"metadata": map[string]any{
			"name": "candidate-policy", "version": "0.1.0",
		},
		"spec": map[string]any{
			"outcomes":       []string{"allow", "review"},
			"defaultOutcome": "review",
			"questions": map[string]any{
				"risky": map[string]any{
					"type":         "noul",
					"instructions": "Is this risky?",
				},
			},
		},
	}

	res := api.do(t, http.MethodPost, "/v1/policies/validate",
		map[string]any{"policy": candidate}, nil)
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))

	report := res.Map(t)
	assert.Equal(t, true, report["valid"])

	projected, ok := report["policy"].(map[string]any)
	require.True(t, ok, "a valid document reports the policy it compiled to")
	assert.NotEmpty(
		t, projected["hash"],
		"the compiled policy is identified by its canonical hash",
	)

	listed := api.do(t, http.MethodGet, "/v1/policies", nil, nil)
	items, ok := listed.Map(t)["items"].([]any)
	require.True(t, ok)
	assert.Len(
		t, items, 1,
		"validating a document must not add it to the loaded set",
	)
}

func TestValidatePolicyRejectsABrokenDocument(t *testing.T) {
	api := newClient(sharedApp)

	broken := map[string]any{
		"apiVersion": "vibecheck.psyb0t.dev/v1alpha1",
		"kind":       "DecisionPolicy",
		"metadata": map[string]any{
			"name": "broken-policy", "version": "0.1.0",
		},
		"spec": map[string]any{
			"outcomes": []string{"allow", "review"},
			// Not one of the declared outcomes.
			"defaultOutcome": "escalate",
			"questions": map[string]any{
				"risky": map[string]any{
					"type":         "noul",
					"instructions": "Is this risky?",
				},
			},
		},
	}

	res := api.do(t, http.MethodPost, "/v1/policies/validate",
		map[string]any{"policy": broken}, nil)

	// The contract answers 200 with a verdict. "this does not compile" is a
	// successful answer to "does this compile", and the reason is the
	// payload the caller asked for.
	require.Equal(t, http.StatusOK, res.Status, string(res.Body))

	report := res.Map(t)
	assert.Equal(t, false, report["valid"])
	assert.NotEmpty(
		t, report["error"],
		"an invalid document reports why it failed to compile",
	)
	assert.Nil(
		t, report["policy"],
		"a document that does not compile has no compiled projection",
	)
}

func TestHealthAndReadinessAreServedUnversioned(t *testing.T) {
	api := newClient(sharedApp)

	for _, path := range []string{"/healthz", "/ready"} {
		t.Run(path, func(t *testing.T) {
			res := api.do(t, http.MethodGet, path, nil, nil)
			assert.Equal(t, http.StatusOK, res.Status)
		})
	}
}

func TestRequestIDIsEchoedAndSanitised(t *testing.T) {
	api := newClient(sharedApp)

	t.Run("a supplied uuid is echoed", func(t *testing.T) {
		supplied := uuid.NewString()

		res := api.do(t, http.MethodGet, "/v1/policies", nil,
			map[string]string{headerRequestID: supplied})

		assert.Equal(t, supplied, res.Header.Get(headerRequestID))
	})

	t.Run("junk is replaced rather than echoed", func(t *testing.T) {
		// No newline here on purpose. net/http refuses to transmit a
		// header value containing one, so a newline probe would only
		// prove the client's guard, never the server's.
		junk := "not-an-id injected=line"

		res := api.do(t, http.MethodGet, "/v1/policies", nil,
			map[string]string{headerRequestID: junk})

		echoed := res.Header.Get(headerRequestID)
		assert.NotEqual(t, junk, echoed)
		_, err := uuid.Parse(echoed)
		assert.NoError(t, err, "a generated request id is a UUID")
	})
}
