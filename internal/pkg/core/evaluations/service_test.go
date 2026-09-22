package evaluations_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/core/evaluations"
	"github.com/psyb0t/vibecheck/internal/pkg/core/policies"
	"github.com/psyb0t/vibecheck/internal/pkg/db"
	"github.com/psyb0t/vibecheck/internal/pkg/db/repositories"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	"github.com/psyb0t/vibecheck/internal/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	policyName    = "test-policy"
	policyVersion = "1.0.0"

	questionRisky = "risky"
	questionClass = "actionClass"

	factDestructive = "action.destructive"
	factOwned       = "target.ownedBySession"

	outcomeAllow  = "allow"
	outcomeReview = "review"
	outcomeBlock  = "block"

	rulePreBlock    = "block-unowned-destructive"
	ruleAllowSafe   = "allow-safe"
	scriptedModelID = "jev-1.13.0"
)

// policyYAML is the fixture every case runs against. It has a deterministic
// pre-rule and one decision rule, so both execution paths are reachable.
const policyYAML = `apiVersion: vibecheck.psyb0t.dev/v1alpha1
kind: DecisionPolicy
metadata:
  name: test-policy
  version: "1.0.0"
spec:
  model: jev-latest
  outcomes: [allow, review, block]
  defaultOutcome: review
  input:
    requiredFacts:
      action.destructive: boolean
      target.ownedBySession: boolean
  preRules:
    - id: block-unowned-destructive
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
    risky:
      type: noul
      instructions: Is this risky?
    actionClass:
      type: choice
      instructions: Which class?
      criteria:
        read: reads state
        write: changes state
  decisionRules:
    - id: allow-safe
      when:
        all:
          - left: {answer: risky, field: noul}
            op: lt
            right: 0.35
          - left: {answer: actionClass, field: choice}
            op: eq
            right: read
      outcome: allow
`

// scriptedProvider is the injected provider boundary. It returns exactly what
// a test tells it to and counts calls, so a test can assert that a pre-rule
// short-circuit really did skip the model.
type scriptedProvider struct {
	mu       sync.Mutex
	calls    int
	requests []provider.Request

	response provider.Response
	err      error
}

func (p *scriptedProvider) Evaluate(
	_ context.Context,
	request provider.Request,
) (provider.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	p.requests = append(p.requests, request)

	if p.err != nil {
		return provider.Response{}, p.err
	}

	return p.response, nil
}

func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.calls
}

// answeringProvider returns a well-formed response steering the decision rule.
func answeringProvider(noul float64, choice string) *scriptedProvider {
	return &scriptedProvider{
		response: provider.Response{
			Model: scriptedModelID,
			Answers: decision.Answers{
				questionRisky: {
					Type: decision.QuestionTypeNoul,
					Noul: noul,
				},
				questionClass: {
					Type:          decision.QuestionTypeChoice,
					Choice:        choice,
					Confidence:    0.9,
					HasConfidence: true,
					Probabilities: map[string]float64{
						"read":  0.9,
						"write": 0.1,
					},
				},
			},
			Usage:    provider.Usage{InputTokens: 100, OutputTokens: 20},
			Attempts: []provider.Attempt{{Number: 1}},
			Duration: 5 * time.Millisecond,
		},
	}
}

type harness struct {
	service  *evaluations.Service
	provider *scriptedProvider
	repo     *repositories.Query
	observer *observerSpy
	clock    time.Time
}

type observerSpy struct {
	providerStarted  int
	providerFinished int
	providerAttempts int
	model            string
	inputTokens      int
	outputTokens     int
	evaluations      int
	policyKind       string
	outcome          string
	executionPath    string
}

func (o *observerSpy) ProviderCallStarted() {
	o.providerStarted++
}

func (o *observerSpy) ProviderCallFinished() {
	o.providerFinished++
}

func (o *observerSpy) ObserveProviderCall(attempts int) {
	o.providerAttempts = attempts
}

func (o *observerSpy) ObserveTokens(model string, input, output int) {
	o.model = model
	o.inputTokens = input
	o.outputTokens = output
}

func (o *observerSpy) ObserveEvaluation(
	policyKind, outcome, executionPath string,
	_ float64,
) {
	o.evaluations++
	o.policyKind = policyKind
	o.outcome = outcome
	o.executionPath = executionPath
}

// newHarness wires the real service against a real SQLite database with the
// production migrations applied. Only the provider is scripted.
func newHarness(
	t *testing.T,
	decisionProvider *scriptedProvider,
	adjust func(*evaluations.Config),
) *harness {
	t.Helper()

	database, err := db.Open(t.Context(), db.Config{
		Driver:     db.DriverSQLite,
		SQLitePath: filepath.Join(t.TempDir(), "vibecheck.sqlite"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	compiled, err := policy.CompileBytes([]byte(policyYAML), 0)
	require.NoError(t, err)

	set, err := policy.NewSet([]*policy.Compiled{compiled})
	require.NoError(t, err)

	config := evaluations.Config{
		MaxStateBytes:          1024,
		MaxFacts:               16,
		MaxMetadataEntries:     8,
		MaxMetadataValueLength: 64,
		MaxPolicyBytes:         policy.DefaultMaxPolicyBytes,
		IdempotencyRetention:   time.Hour,
	}

	if adjust != nil {
		adjust(&config)
	}

	repo := repositories.Use(database.Gorm)

	harness := &harness{
		provider: decisionProvider,
		repo:     repo,
		observer: &observerSpy{},
		clock:    time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC),
	}

	harness.service = evaluations.New(
		policies.New(set, policy.DefaultMaxPolicyBytes),
		decisionProvider,
		repo,
		nil,
		config,
		evaluations.WithClock(func() time.Time { return harness.clock }),
		evaluations.WithObserver(harness.observer),
	)

	return harness
}

func TestCreateRecordsProviderAndEvaluationObservability(t *testing.T) {
	provider := answeringProvider(0.1, "read")
	h := newHarness(t, provider, nil)

	result, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	assert.Equal(t, 1, h.observer.providerStarted)
	assert.Equal(t, 1, h.observer.providerFinished)
	assert.Equal(t, 1, h.observer.providerAttempts)
	assert.Equal(t, scriptedModelID, h.observer.model)
	assert.Equal(t, 100, h.observer.inputTokens)
	assert.Equal(t, 20, h.observer.outputTokens)
	assert.Equal(t, 1, h.observer.evaluations)
	assert.Equal(t, evaluations.PolicyKindNamed.String(), h.observer.policyKind)
	assert.Equal(t, result.Outcome, h.observer.outcome)
	assert.Equal(t, string(result.ExecutionPath), h.observer.executionPath)
}

func namedRef() *policy.Ref {
	return &policy.Ref{Name: policyName, Version: policyVersion}
}

func safeInput() evaluations.CreateInput {
	return evaluations.CreateInput{
		PolicyRef: namedRef(),
		State:     "a synthetic state",
		Facts: decision.Facts{
			factDestructive: false,
			factOwned:       true,
		},
	}
}

func TestCreateEnforcesTheConfiguredStateSize(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		state     any
		maxBytes  int
		wantError bool
	}{
		{
			name:     "exact serialized limit",
			state:    "four",
			maxBytes: len(`"four"`),
		},
		{
			name:      "one byte over serialized limit",
			state:     "four",
			maxBytes:  len(`"four"`) - 1,
			wantError: true,
		},
		{
			name:      "structured state uses encoded size",
			state:     map[string]any{"key": "value"},
			maxBytes:  14,
			wantError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(
				t,
				answeringProvider(0.1, "read"),
				func(config *evaluations.Config) {
					config.MaxStateBytes = tc.maxBytes
				},
			)

			input := safeInput()
			input.State = tc.state

			_, err := h.service.Create(t.Context(), input)
			if tc.wantError {
				require.ErrorIs(t, err, commerr.ErrValidationFailed)
				assert.Equal(t, 0, h.provider.callCount())

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestCreateShortCircuitsOnAPreRuleWithoutCallingTheProvider(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	input := safeInput()
	input.Facts[factDestructive] = true
	input.Facts[factOwned] = false

	result, err := h.service.Create(t.Context(), input)
	require.NoError(t, err)

	assert.Equal(t, outcomeBlock, result.Outcome)
	assert.Equal(t, api.ExecutionPath("pre_rule"), result.ExecutionPath)
	require.NotNil(t, result.MatchedRuleId)
	assert.Equal(t, rulePreBlock, *result.MatchedRuleId)
	assert.Equal(t, api.EvaluationStatus("completed"), result.Status)
	assert.Equal(
		t, 0, h.provider.callCount(),
		"the whole point of a pre-rule is that it does not pay for a model call", //nolint:lll // assertion message stays intact; splitting a string literal to satisfy line length is banned
	)
	assert.Empty(t, result.Answers)
}

func TestCreateRunsTheModelAndAppliesADecisionRule(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	result, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	assert.Equal(t, outcomeAllow, result.Outcome)
	assert.Equal(t, api.ExecutionPath("decision_rule"), result.ExecutionPath)
	require.NotNil(t, result.MatchedRuleId)
	assert.Equal(t, ruleAllowSafe, *result.MatchedRuleId)
	assert.Equal(t, 1, h.provider.callCount())

	require.NotNil(t, result.ResolvedModel)
	assert.Equal(t, scriptedModelID, *result.ResolvedModel)
	assert.Equal(t, 100, result.Usage.InputTokens)
	assert.Equal(t, 20, result.Usage.OutputTokens)
	assert.Len(t, result.Answers, 2)
	assert.Equal(t, api.PolicyKind("named"), result.PolicyKind)
}

func TestCreateFallsBackToTheDefaultOutcome(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.9, "write"), nil)

	result, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	assert.Equal(t, outcomeReview, result.Outcome)
	assert.Equal(t, api.ExecutionPath("default"), result.ExecutionPath)
	assert.Nil(
		t, result.MatchedRuleId,
		"the default outcome is not a rule, so nothing is reported as matching",
	)
}

func TestCreateSendsEveryQuestionInOneRequest(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	_, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	require.Len(t, h.provider.requests, 1)
	request := h.provider.requests[0]
	assert.Equal(t, "a synthetic state", request.State)
	assert.Equal(t, "jev-latest", request.Model)
	require.Len(t, request.Questions, 2)
	assert.Equal(
		t,
		[]string{questionClass, questionRisky},
		[]string{request.Questions[0].ID, request.Questions[1].ID},
		"questions go out in the policy's deterministic order",
	)
}

func TestCreateRejectsInputThatFailsThePolicyContract(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		facts decision.Facts
	}{
		{
			name:  "missing required fact",
			facts: decision.Facts{factDestructive: false},
		},
		{
			name: "wrongly typed fact",
			facts: decision.Facts{
				factDestructive: "false",
				factOwned:       true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, answeringProvider(0.1, "read"), nil)

			input := safeInput()
			input.Facts = tc.facts

			_, err := h.service.Create(t.Context(), input)
			require.ErrorIs(t, err, commerr.ErrValidationFailed)
			assert.Equal(t, 0, h.provider.callCount())
		})
	}
}

func TestCreateRecordsAProviderFailureRatherThanLosingIt(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{
			name:     "rate limited",
			err:      ctxerrors.Wrap(commerr.ErrRateLimited, "provider"),
			wantCode: "PROVIDER_RATE_LIMITED",
		},
		{
			name:     "unauthenticated",
			err:      ctxerrors.Wrap(commerr.ErrNotAuthenticated, "provider"),
			wantCode: "PROVIDER_AUTH_FAILED",
		},
		{
			name:     "timeout",
			err:      ctxerrors.Wrap(commerr.ErrTimeout, "provider"),
			wantCode: "PROVIDER_TIMEOUT",
		},
		{
			name:     "unavailable",
			err:      ctxerrors.Wrap(commerr.ErrUnavailable, "provider"),
			wantCode: "PROVIDER_UNAVAILABLE",
		},
		{
			name:     "malformed response",
			err:      ctxerrors.Wrap(commerr.ErrParseFailed, "provider"),
			wantCode: "PROVIDER_PROTOCOL_ERROR",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, &scriptedProvider{err: tc.err}, nil)

			result, err := h.service.Create(t.Context(), safeInput())
			require.NoError(
				t, err,
				"a provider failure is a recorded outcome, not a dropped request", //nolint:lll // assertion message stays intact; splitting a string literal to satisfy line length is banned
			)

			assert.Equal(
				t,
				api.EvaluationStatus("provider_failed"),
				result.Status,
			)
			require.NotNil(t, result.FailureCode)
			assert.Equal(t, tc.wantCode, *result.FailureCode)

			stored, err := h.service.Get(t.Context(), result.Id)
			require.NoError(t, err, "the failure must still be auditable")
			assert.Equal(t, result.Id, stored.Id)
		})
	}
}

func TestCreateRecordsAProviderContractViolation(t *testing.T) {
	t.Parallel()

	broken := answeringProvider(0.1, "read")
	// The policy declares two questions; answering one breaks the contract.
	delete(broken.response.Answers, questionClass)

	h := newHarness(t, broken, nil)

	result, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	assert.Equal(t, api.EvaluationStatus("provider_failed"), result.Status)
	require.NotNil(t, result.FailureCode)
	assert.Equal(t, "PROVIDER_PROTOCOL_ERROR", *result.FailureCode)
}

func TestCreateRequiresExactlyOnePolicySelector(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		mutate func(*evaluations.CreateInput)
	}{
		{
			name:   "neither",
			mutate: func(i *evaluations.CreateInput) { i.PolicyRef = nil },
		},
		{
			name: "both",
			mutate: func(i *evaluations.CreateInput) {
				i.InlinePolicy = map[string]any{"apiVersion": "x"}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, answeringProvider(0.1, "read"), nil)

			input := safeInput()
			tc.mutate(&input)

			_, err := h.service.Create(t.Context(), input)
			require.ErrorIs(t, err, evaluations.ErrPolicySelection)
		})
	}
}

func TestCreateRefusesAnUnknownPolicy(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	input := safeInput()
	input.PolicyRef = &policy.Ref{Name: policyName, Version: "9.9.9"}

	_, err := h.service.Create(t.Context(), input)
	require.ErrorIs(t, err, commerr.ErrNotFound)
}

func TestInlinePoliciesAreRefusedUnlessEnabled(t *testing.T) {
	t.Parallel()

	inline := inlineDocument(t)

	t.Run("disabled by default", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t, answeringProvider(0.1, "read"), nil)

		_, err := h.service.Create(t.Context(), evaluations.CreateInput{
			InlinePolicy: inline,
			State:        "x",
			Facts: decision.Facts{
				factDestructive: false,
				factOwned:       true,
			},
		})
		require.ErrorIs(t, err, evaluations.ErrInlinePoliciesDisabled)
	})

	t.Run("enabled by an operator", func(t *testing.T) {
		t.Parallel()

		h := newHarness(
			t, answeringProvider(0.1, "read"), func(c *evaluations.Config) {
				c.AllowInlinePolicies = true
			},
		)

		result, err := h.service.Create(t.Context(), evaluations.CreateInput{
			InlinePolicy: inline,
			State:        "x",
			Facts: decision.Facts{
				factDestructive: false,
				factOwned:       true,
			},
		})
		require.NoError(t, err)

		assert.Equal(t, api.PolicyKind("inline"), result.PolicyKind)
		assert.Equal(t, outcomeAllow, result.Outcome)
		assert.NotEmpty(
			t, result.PolicyHash,
			"an inline decision is snapshotted too, so it stays reconstructable", //nolint:lll // assertion message stays intact; splitting a string literal to satisfy line length is banned
		)
	})
}

func TestIdempotentReplayReturnsTheOriginalEvaluation(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	input := safeInput()
	input.IdempotencyKey = uuid.NewString()

	first, err := h.service.Create(t.Context(), input)
	require.NoError(t, err)

	second, err := h.service.Create(t.Context(), input)
	require.NoError(t, err)

	assert.Equal(t, first.Id, second.Id)
	assert.Equal(
		t, 1, h.provider.callCount(),
		"a replay must not spend another provider call",
	)
}

func TestIdempotencyKeyReuseWithADifferentRequestConflicts(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	key := uuid.NewString()

	first := safeInput()
	first.IdempotencyKey = key

	_, err := h.service.Create(t.Context(), first)
	require.NoError(t, err)

	second := safeInput()
	second.IdempotencyKey = key
	second.State = "a different state"

	_, err = h.service.Create(t.Context(), second)
	require.ErrorIs(t, err, evaluations.ErrIdempotencyConflict)
}

func TestGetReportsAMissingEvaluation(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	_, err := h.service.Get(t.Context(), uuid.New())
	require.ErrorIs(t, err, commerr.ErrNotFound)
}

func TestListWalksEveryPage(t *testing.T) {
	t.Parallel()

	const (
		total = 13
		limit = 5
	)

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	created := make([]uuid.UUID, 0, total)

	for i := range total {
		// Each row gets a distinct timestamp so the newest-first ordering
		// is deterministic rather than dependent on insert order.
		h.clock = h.clock.Add(time.Second)

		input := safeInput()
		input.Metadata = map[string]string{"seq": strconv.Itoa(i)}

		result, err := h.service.Create(t.Context(), input)
		require.NoError(t, err)

		created = append(created, result.Id)
	}

	wantPageSizes := []int{5, 5, 3}
	seen := make([]uuid.UUID, 0, total)

	for page, wantSize := range wantPageSizes {
		offset := page * limit

		listed, err := h.service.List(t.Context(), evaluations.ListFilter{
			Limit:  limit,
			Offset: offset,
		})
		require.NoError(t, err)

		assert.Len(t, listed.Items, wantSize)
		assert.Equal(t, limit, listed.Limit)
		assert.Equal(t, offset, listed.Offset)
		assert.Equal(t, page < len(wantPageSizes)-1, listed.HasMore)

		for _, item := range listed.Items {
			seen = append(seen, item.Id)
		}
	}

	require.Len(t, seen, total)

	// Newest first, so the walk is the creation order reversed.
	for i, id := range seen {
		assert.Equal(t, created[total-1-i], id)
	}

	past, err := h.service.List(t.Context(), evaluations.ListFilter{
		Limit:  limit,
		Offset: total * 2,
	})
	require.NoError(t, err)
	assert.Empty(t, past.Items)
	assert.False(t, past.HasMore)
}

func TestListFiltersAreApplied(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	allowed := safeInput()
	_, err := h.service.Create(t.Context(), allowed)
	require.NoError(t, err)

	blocked := safeInput()
	blocked.Facts[factDestructive] = true
	blocked.Facts[factOwned] = false
	_, err = h.service.Create(t.Context(), blocked)
	require.NoError(t, err)

	listed, err := h.service.List(t.Context(), evaluations.ListFilter{
		Limit:   10,
		Outcome: outcomeBlock,
	})
	require.NoError(t, err)

	require.Len(t, listed.Items, 1)
	assert.Equal(t, outcomeBlock, listed.Items[0].Outcome)
}

func TestListRejectsAnInvalidPage(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	testCases := []struct {
		name   string
		filter evaluations.ListFilter
	}{
		{"zero limit", evaluations.ListFilter{Limit: 0}},
		{"negative limit", evaluations.ListFilter{Limit: -1}},
		{"negative offset", evaluations.ListFilter{Limit: 5, Offset: -1}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := h.service.List(t.Context(), tc.filter)
			require.ErrorIs(t, err, commerr.ErrInvalidArgument)
		})
	}
}

func TestRetentionStoresOnlyCiphertext(t *testing.T) {
	t.Parallel()

	const secretState = "SENSITIVE-STATE-THAT-MUST-NOT-BE-READABLE"

	key, err := secrets.GenerateDataKey()
	require.NoError(t, err)

	sealer, err := secrets.NewSealer(key)
	require.NoError(t, err)

	database, err := db.Open(t.Context(), db.Config{
		Driver:     db.DriverSQLite,
		SQLitePath: filepath.Join(t.TempDir(), "vibecheck.sqlite"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	compiled, err := policy.CompileBytes([]byte(policyYAML), 0)
	require.NoError(t, err)

	set, err := policy.NewSet([]*policy.Compiled{compiled})
	require.NoError(t, err)

	repo := repositories.Use(database.Gorm)

	service := evaluations.New(
		policies.New(set, policy.DefaultMaxPolicyBytes),
		answeringProvider(0.1, "read"),
		repo,
		sealer,
		evaluations.Config{
			MaxFacts:             16,
			StoreInputs:          true,
			IdempotencyRetention: time.Hour,
		},
	)

	input := safeInput()
	input.State = secretState

	result, err := service.Create(t.Context(), input)
	require.NoError(t, err)

	stored, err := repo.Evaluation.WithContext(t.Context()).
		Where(repo.Evaluation.ID.Eq(result.Id)).
		First()
	require.NoError(t, err)

	require.NotEmpty(t, stored.EncryptedInput)
	assert.NotContains(
		t, string(stored.EncryptedInput), secretState,
		"retained input must be unreadable without the data key",
	)

	opened, err := sealer.Open(stored.EncryptedInput)
	require.NoError(t, err)
	assert.Contains(t, string(opened), secretState)
}

func TestRetentionOffStoresNothing(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	result, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	stored, err := h.repo.Evaluation.WithContext(t.Context()).
		Where(h.repo.Evaluation.ID.Eq(result.Id)).
		First()
	require.NoError(t, err)

	assert.Empty(
		t, stored.EncryptedInput,
		"with retention off, the raw request is not written anywhere",
	)
}

func TestSubmitFeedbackRecordsALabel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	evaluation, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	feedback, err := h.service.SubmitFeedback(
		t.Context(),
		evaluations.FeedbackInput{
			EvaluationID:    evaluation.Id,
			ExpectedOutcome: outcomeReview,
			Note:            "a human disagreed",
		},
	)
	require.NoError(t, err)

	assert.Equal(t, evaluation.Id, feedback.EvaluationId)
	assert.Equal(t, outcomeReview, feedback.ExpectedOutcome)

	unchanged, err := h.service.Get(t.Context(), evaluation.Id)
	require.NoError(t, err)
	assert.Equal(
		t, evaluation.Outcome, unchanged.Outcome,
		"feedback is calibration data and must never edit the decision",
	)
}

func TestSubmitFeedbackRejectsBadInput(t *testing.T) {
	t.Parallel()

	h := newHarness(t, answeringProvider(0.1, "read"), nil)

	evaluation, err := h.service.Create(t.Context(), safeInput())
	require.NoError(t, err)

	t.Run("unknown evaluation", func(t *testing.T) {
		t.Parallel()

		_, err := h.service.SubmitFeedback(
			t.Context(),
			evaluations.FeedbackInput{
				EvaluationID:    uuid.New(),
				ExpectedOutcome: outcomeAllow,
			},
		)
		require.ErrorIs(t, err, commerr.ErrNotFound)
	})

	t.Run("malformed outcome", func(t *testing.T) {
		t.Parallel()

		_, err := h.service.SubmitFeedback(
			t.Context(),
			evaluations.FeedbackInput{
				EvaluationID:    evaluation.Id,
				ExpectedOutcome: "not a valid outcome",
			},
		)
		require.ErrorIs(t, err, commerr.ErrValidationFailed)
	})

	t.Run("oversized note", func(t *testing.T) {
		t.Parallel()

		_, err := h.service.SubmitFeedback(
			t.Context(),
			evaluations.FeedbackInput{
				EvaluationID:    evaluation.Id,
				ExpectedOutcome: outcomeAllow,
				Note: string(
					make([]byte, evaluations.MaxFeedbackNoteLength+1),
				),
			},
		)
		require.ErrorIs(t, err, commerr.ErrValidationFailed)
	})
}

func TestCreateRejectsOversizedCallerInput(t *testing.T) {
	t.Parallel()

	h := newHarness(
		t, answeringProvider(0.1, "read"), func(c *evaluations.Config) {
			c.MaxFacts = 2
			c.MaxMetadataEntries = 1
			c.MaxMetadataValueLength = 4
		},
	)

	t.Run("too many facts", func(t *testing.T) {
		t.Parallel()

		input := safeInput()
		input.Facts["extra.one"] = true
		input.Facts["extra.two"] = true

		_, err := h.service.Create(t.Context(), input)
		require.ErrorIs(t, err, commerr.ErrValidationFailed)
	})

	t.Run("non-scalar fact", func(t *testing.T) {
		t.Parallel()

		input := safeInput()
		input.Facts[factOwned] = map[string]any{"nested": true}

		_, err := h.service.Create(t.Context(), input)
		require.ErrorIs(t, err, commerr.ErrValidationFailed)
	})

	t.Run("oversized metadata value", func(t *testing.T) {
		t.Parallel()

		input := safeInput()
		input.Metadata = map[string]string{"caller": "far too long"}

		_, err := h.service.Create(t.Context(), input)
		require.ErrorIs(t, err, commerr.ErrValidationFailed)
	})
}

// inlineDocument returns the fixture policy decoded as a generic document,
// which is the shape an inline policy arrives in.
func inlineDocument(t *testing.T) map[string]any {
	t.Helper()

	return map[string]any{
		"apiVersion": policy.APIVersion,
		"kind":       policy.Kind,
		"metadata": map[string]any{
			"name":    "inline-policy",
			"version": "1.0.0",
		},
		"spec": map[string]any{
			"outcomes":       []any{outcomeAllow, outcomeReview},
			"defaultOutcome": outcomeReview,
			"input": map[string]any{
				"requiredFacts": map[string]any{
					factDestructive: "boolean",
					factOwned:       "boolean",
				},
			},
			"questions": map[string]any{
				questionRisky: map[string]any{
					"type":         "noul",
					"instructions": "Is this risky?",
				},
				questionClass: map[string]any{
					"type":         "choice",
					"instructions": "Which class?",
					"criteria": map[string]any{
						"read":  "reads state",
						"write": "changes state",
					},
				},
			},
			"decisionRules": []any{
				map[string]any{
					"id": "allow-safe",
					"when": map[string]any{
						"left": map[string]any{
							"answer": questionRisky,
							"field":  "noul",
						},
						"op":    "lt",
						"right": 0.35,
					},
					"outcome": outcomeAllow,
				},
			},
		},
	}
}

// The state ceiling has to bite on its own, not only as a side effect of the
// request-body limit. A caller can send a small envelope whose state field is
// still far larger than the deployment wants to hand a model.
func TestStateOverTheCeilingIsRefused(t *testing.T) {
	t.Parallel()

	const ceiling = 256

	testCases := []struct {
		name    string
		state   any
		wantErr bool
	}{
		{
			name:    "a string inside the ceiling",
			state:   strings.Repeat("a", ceiling/2),
			wantErr: false,
		},
		{
			name:    "a string over the ceiling",
			state:   strings.Repeat("a", ceiling*4),
			wantErr: true,
		},
		{
			name: "a structured value over the ceiling",
			state: map[string]any{
				"blob": strings.Repeat("b", ceiling*4),
			},
			wantErr: true,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(
				t, answeringProvider(0.1, "read"),
				func(c *evaluations.Config) { c.MaxStateBytes = ceiling },
			)

			_, err := h.service.Create(t.Context(), evaluations.CreateInput{
				PolicyRef: &policy.Ref{Name: policyName, Version: "1.0.0"},
				State:     testCase.state,
				Facts: decision.Facts{
					factDestructive: false,
					factOwned:       true,
				},
			})

			if !testCase.wantErr {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, commerr.ErrValidationFailed)
		})
	}
}

// An oversized state must be refused before the provider is asked, or the
// ceiling costs a billed call to enforce.
func TestStateOverTheCeilingNeverReachesTheProvider(t *testing.T) {
	t.Parallel()

	decisionProvider := answeringProvider(0.1, "read")

	h := newHarness(t, decisionProvider, func(c *evaluations.Config) {
		c.MaxStateBytes = 64
	})

	_, err := h.service.Create(t.Context(), evaluations.CreateInput{
		PolicyRef: &policy.Ref{Name: policyName, Version: "1.0.0"},
		State:     strings.Repeat("a", 4096),
		Facts: decision.Facts{
			factDestructive: false,
			factOwned:       true,
		},
	})
	require.ErrorIs(t, err, commerr.ErrValidationFailed)

	decisionProvider.mu.Lock()
	defer decisionProvider.mu.Unlock()

	assert.Zero(
		t, decisionProvider.calls,
		"a request refused on size must not be billed",
	)
}

// The question ceiling is what stops one inline policy from turning a single
// request into an unbounded amount of model work.
func TestInlinePolicyQuestionCeilingIsEnforced(t *testing.T) {
	t.Parallel()

	inline := inlineDocument(t)

	spec, ok := inline["spec"].(map[string]any)
	require.True(t, ok)

	questions, ok := spec["questions"].(map[string]any)
	require.True(t, ok)

	require.Len(
		t, questions, 2,
		"the fixture declares the two questions this ceiling is set against",
	)

	h := newHarness(
		t, answeringProvider(0.1, "read"), func(c *evaluations.Config) {
			c.AllowInlinePolicies = true
			c.MaxQuestions = 1
		},
	)

	_, err := h.service.Create(t.Context(), evaluations.CreateInput{
		InlinePolicy: inline,
		State:        "x",
		Facts: decision.Facts{
			factDestructive: false,
			factOwned:       true,
		},
	})
	require.Error(
		t, err, "two questions cannot compile under a ceiling of one",
	)
}

// The same inline policy compiles once the ceiling allows its questions, so
// the refusal above is the ceiling and not a broken fixture.
func TestInlinePolicyCompilesWithinTheQuestionCeiling(t *testing.T) {
	t.Parallel()

	h := newHarness(
		t, answeringProvider(0.1, "read"), func(c *evaluations.Config) {
			c.AllowInlinePolicies = true
			c.MaxQuestions = 2
		},
	)

	result, err := h.service.Create(t.Context(), evaluations.CreateInput{
		InlinePolicy: inlineDocument(t),
		State:        "x",
		Facts: decision.Facts{
			factDestructive: false,
			factOwned:       true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, api.PolicyKind("inline"), result.PolicyKind)
}
