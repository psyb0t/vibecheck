// Package evaluations owns the evaluation pipeline.
//
// It is the only place that sequences policy resolution, input validation,
// deterministic pre-rules, the provider call, provider-response validation,
// decision rules, and persistence. Both transports call the same methods
// here, which is what makes REST and MCP produce identical decisions and
// identical audit rows for the same request.
package evaluations

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/core/policies"
	"github.com/psyb0t/vibecheck/internal/pkg/db/models"
	"github.com/psyb0t/vibecheck/internal/pkg/db/repositories"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	"github.com/psyb0t/vibecheck/internal/pkg/secrets"
)

// PolicyKind is the bounded label recorded on an evaluation and used as a
// metric dimension. The exact policy identity stays in the row and the logs.
type PolicyKind string

const (
	PolicyKindNamed  PolicyKind = "named"
	PolicyKindInline PolicyKind = "inline"
)

func (k PolicyKind) String() string {
	return string(k)
}

// Config is the evaluation pipeline's deployment configuration.
type Config struct {
	// AllowInlinePolicies lets a request carry a complete policy document.
	// Off by default: it widens the attack surface from "pick a loaded
	// policy" to "supply arbitrary rules".
	AllowInlinePolicies bool

	// MaxPolicyBytes bounds an inline policy document.
	MaxPolicyBytes int

	// MaxStateBytes bounds the JSON representation of caller state.
	MaxStateBytes int

	// MaxQuestions bounds the questions in an inline policy.
	MaxQuestions int

	// MaxFacts and MaxMetadataEntries bound caller-supplied maps.
	MaxFacts           int
	MaxMetadataEntries int

	// MaxMetadataValueLength bounds one metadata value.
	MaxMetadataValueLength int

	// StoreInputs turns on encrypted retention of the raw request.
	StoreInputs bool

	// IdempotencyRetention is how long a key stays replayable.
	IdempotencyRetention time.Duration
}

// Observer receives bounded evaluation and provider measurements. Raw state,
// facts, metadata, policy identities, and request identities never cross it.
type Observer interface {
	ProviderCallStarted()
	ProviderCallFinished()
	ObserveProviderCall(attempts int)
	ObserveTokens(model string, input, output int)
	ObserveEvaluation(
		policyKind, outcome, executionPath string,
		seconds float64,
	)
}

// Service runs evaluations.
type Service struct {
	policies *policies.Service
	provider provider.Provider
	repo     *repositories.Query
	sealer   *secrets.Sealer
	config   Config
	observer Observer

	// now and newID are injectable so tests can assert exact persisted
	// values instead of matching on "some uuid, some timestamp".
	now   func() time.Time
	newID func() uuid.UUID
}

// Option adjusts a service at construction.
type Option func(*Service)

// WithClock replaces the time source.
func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		s.now = now
	}
}

// WithIDs replaces the identifier source.
func WithIDs(newID func() uuid.UUID) Option {
	return func(s *Service) {
		s.newID = newID
	}
}

// WithObserver records pipeline measurements without coupling the core to a
// concrete metrics implementation.
func WithObserver(observer Observer) Option {
	return func(s *Service) {
		s.observer = observer
	}
}

// New builds the evaluation service.
func New(
	policyService *policies.Service,
	decisionProvider provider.Provider,
	repo *repositories.Query,
	sealer *secrets.Sealer,
	config Config,
	options ...Option,
) *Service {
	service := &Service{
		policies: policyService,
		provider: decisionProvider,
		repo:     repo,
		sealer:   sealer,
		config:   config,
		now:      time.Now,
		newID:    uuid.New,
	}

	for _, option := range options {
		option(service)
	}

	return service
}

// CreateInput is one evaluation request, already decoded and free of any
// transport concern.
type CreateInput struct {
	// PolicyRef selects a loaded policy. Exactly one of PolicyRef and
	// InlinePolicy must be set.
	PolicyRef *policy.Ref

	// InlinePolicy is a complete policy document supplied by the caller.
	InlinePolicy map[string]any

	State    any
	Facts    decision.Facts
	Metadata map[string]string

	// IdempotencyKey is the caller's replay token, empty when absent.
	IdempotencyKey string

	// RequestID correlates this evaluation with the request that caused it.
	RequestID string
}

// Create runs one evaluation end to end and returns the recorded result.
//
// The evaluation is persisted before this returns, so a caller that got a
// response can always find the audit row. A provider failure is still
// recorded, with a status and a failure code, rather than vanishing.
func (s *Service) Create(
	ctx context.Context,
	input CreateInput,
) (api.Evaluation, error) {
	compiled, kind, err := s.resolvePolicy(input)
	if err != nil {
		return api.Evaluation{}, err
	}

	if err := s.validateRequestShape(input); err != nil {
		return api.Evaluation{}, err
	}

	requestHash, err := canonicalRequestHash(compiled.Hash, input)
	if err != nil {
		return api.Evaluation{}, err
	}

	if replayed, found, err := s.replay(ctx, input, requestHash); err != nil {
		return api.Evaluation{}, err
	} else if found {
		return replayed, nil
	}

	if err := s.ensureSnapshot(ctx, compiled); err != nil {
		return api.Evaluation{}, err
	}

	record, err := s.decide(ctx, compiled, kind, input)
	if err != nil {
		return api.Evaluation{}, err
	}

	if err := s.persist(ctx, record, input, requestHash); err != nil {
		return api.Evaluation{}, err
	}

	s.observeCompleted(record, kind)
	s.logCompleted(ctx, record, compiled, kind)

	return evaluationModelToAPI(record), nil
}

// resolvePolicy picks the compiled policy this request runs against, from the
// loaded set or from an inline document.
func (s *Service) resolvePolicy(
	input CreateInput,
) (*policy.Compiled, PolicyKind, error) {
	hasRef := input.PolicyRef != nil
	hasInline := len(input.InlinePolicy) > 0

	if hasRef == hasInline {
		return nil, "", ctxerrors.Wrap(
			ErrPolicySelection, "policy selection",
		)
	}

	if hasRef {
		compiled, err := s.policies.Compiled(*input.PolicyRef)
		if err != nil {
			return nil, "", ctxerrors.Wrap(err, "resolve the named policy")
		}

		return compiled, PolicyKindNamed, nil
	}

	if !s.config.AllowInlinePolicies {
		return nil, "", ctxerrors.Wrap(
			ErrInlinePoliciesDisabled, "inline policy supplied",
		)
	}

	compiled, err := compileInline(
		input.InlinePolicy,
		s.config.MaxPolicyBytes,
		s.config.MaxQuestions,
	)
	if err != nil {
		return nil, "", err
	}

	return compiled, PolicyKindInline, nil
}

// decide runs the deterministic and model phases and returns the row to
// persist. A provider failure produces a recorded failure, not a lost
// request.
func (s *Service) decide(
	ctx context.Context,
	compiled *policy.Compiled,
	kind PolicyKind,
	input CreateInput,
) (*models.Evaluation, error) {
	startedAt := s.now()

	record := s.newRecord(compiled, kind, input, startedAt)
	s.logStarted(ctx, record, compiled, kind)

	if err := compiled.ValidateFacts(input.Facts); err != nil {
		return nil, ctxerrors.Wrap(err, "validate the supplied facts")
	}

	if s.applyPreRules(ctx, record, compiled, input, startedAt) {
		return record, nil
	}

	response, providerErr := s.askProvider(ctx, record, compiled, input)
	if providerErr != nil {
		return s.recordProviderFailure(record, startedAt, providerErr)
	}

	if err := compiled.ValidateAnswers(response.Answers); err != nil {
		s.recordProtocolFailure(ctx, record, compiled, startedAt, err)

		// An off-contract answer is a recorded failure, not a lost
		// request: the caller still gets an evaluation ID and a durable
		// row carrying the failure code.
		return record, nil
	}

	answersJSON, err := encodeAnswers(response.Answers)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "encode the provider answers")
	}

	record.AnswersJSON = answersJSON

	applyResult(
		record, compiled.EvaluateDecisionRules(input.Facts, response.Answers),
	)
	record.Status = models.EvaluationStatusCompleted.String()
	record.TotalDurationMS = s.elapsedMS(startedAt)

	return record, nil
}

// askProvider makes the one provider call this evaluation is allowed, and
// records what it cost on the row and in the metrics.
//
// The in-flight gauge is paired with a defer so a failed or cancelled call
// still decrements it, which is what keeps the gauge from drifting upward
// over a long-running process.
func (s *Service) askProvider(
	ctx context.Context,
	record *models.Evaluation,
	compiled *policy.Compiled,
	input CreateInput,
) (provider.Response, error) {
	if s.observer != nil {
		s.observer.ProviderCallStarted()
		defer s.observer.ProviderCallFinished()
	}

	response, err := s.provider.Evaluate(
		ctx, buildProviderRequest(compiled, input),
	)
	if err != nil {
		return provider.Response{}, err //nolint:wrapcheck // the caller maps it
	}

	applyUsage(record, &response)

	if s.observer != nil {
		s.observer.ObserveProviderCall(response.AttemptCount())
		s.observer.ObserveTokens(
			response.Model,
			response.Usage.InputTokens,
			response.Usage.OutputTokens,
		)
	}

	return response, nil
}

func (s *Service) observeCompleted(
	record *models.Evaluation,
	kind PolicyKind,
) {
	if s.observer == nil {
		return
	}

	outcome := record.Outcome
	if outcome == "" {
		outcome = record.Status
	}

	executionPath := record.ExecutionPath
	if executionPath == "" {
		executionPath = models.EvaluationStatusProviderFailed.String()
	}

	s.observer.ObserveEvaluation(
		kind.String(),
		outcome,
		executionPath,
		(time.Duration(record.TotalDurationMS) * time.Millisecond).Seconds(),
	)
}

func (s *Service) logStarted(
	ctx context.Context,
	record *models.Evaluation,
	compiled *policy.Compiled,
	kind PolicyKind,
) {
	ctxscope.GetLogger(ctx).Info(
		"evaluation started",
		"evaluation_id", record.ID.String(),
		"policy", compiled.Ref.String(),
		"policy_kind", kind.String(),
	)
}

func (s *Service) logCompleted(
	ctx context.Context,
	record *models.Evaluation,
	compiled *policy.Compiled,
	kind PolicyKind,
) {
	ctxscope.GetLogger(ctx).Info(
		"evaluation completed",
		"evaluation_id", record.ID.String(),
		"policy", compiled.Ref.String(),
		"policy_kind", kind.String(),
		"status", record.Status,
		"outcome", record.Outcome,
		"execution_path", record.ExecutionPath,
		"duration_ms", record.TotalDurationMS,
		"provider_attempts", record.ProviderAttempts,
	)
}

// applyPreRules settles the evaluation from trusted facts alone when a
// pre-rule matches, and reports whether it did. A match means the provider is
// never called, so a policy can refuse on facts without spending a token.
func (s *Service) applyPreRules(
	ctx context.Context,
	record *models.Evaluation,
	compiled *policy.Compiled,
	input CreateInput,
	startedAt time.Time,
) bool {
	preResult, matched := compiled.EvaluatePreRules(input.Facts)
	if !matched {
		return false
	}

	applyResult(record, preResult)
	record.Status = models.EvaluationStatusCompleted.String()
	record.TotalDurationMS = s.elapsedMS(startedAt)

	ctxscope.GetLogger(ctx).Info(
		"evaluation short-circuited",
		"policy", compiled.Ref.String(),
		"outcome", preResult.Outcome.String(),
		"matched_rule_id", preResult.MatchedRuleID,
		"reason", "pre_rule_match",
	)

	return true
}

// applyUsage copies the provider's accounting onto the row so a caller can
// see which model actually answered and what it cost.
func applyUsage(record *models.Evaluation, response *provider.Response) {
	record.ResolvedModel = response.Model
	record.InputTokens = response.Usage.InputTokens
	record.OutputTokens = response.Usage.OutputTokens
	record.ProviderAttempts = response.AttemptCount()
	record.ProviderDurationMS = response.Duration.Milliseconds()
}

// recordProtocolFailure marks the row as a provider protocol failure. The
// answers are discarded: a response that does not satisfy the policy's own
// question contract cannot be scored against its rules.
func (s *Service) recordProtocolFailure(
	ctx context.Context,
	record *models.Evaluation,
	compiled *policy.Compiled,
	startedAt time.Time,
	err error,
) {
	record.Status = models.EvaluationStatusProviderFailed.String()
	record.FailureCode = FailureCodeProviderProtocol.String()
	record.TotalDurationMS = s.elapsedMS(startedAt)

	ctxscope.GetLogger(ctx).Warn(
		"provider answered off-contract",
		"policy", compiled.Ref.String(),
		"err", err,
		"reason", "answer_validation_failed",
	)
}

// recordProviderFailure turns a provider error into a recorded failure with a
// stable code. The provider's own message never reaches the row.
func (s *Service) recordProviderFailure(
	record *models.Evaluation,
	startedAt time.Time,
	providerErr error,
) (*models.Evaluation, error) {
	if errors.Is(providerErr, context.Canceled) {
		return nil, ctxerrors.Wrap(
			providerErr,
			"evaluation cancelled by the caller",
		)
	}

	record.Status = models.EvaluationStatusProviderFailed.String()
	record.FailureCode = classifyProviderFailure(providerErr).String()
	record.TotalDurationMS = s.elapsedMS(startedAt)

	return record, nil
}

// classifyProviderFailure maps a provider error onto a stable code.
func classifyProviderFailure(err error) FailureCode {
	switch {
	case errors.Is(err, commerr.ErrNotAuthenticated),
		errors.Is(err, commerr.ErrRequiredConfigValueNotSet):
		return FailureCodeProviderAuth
	case errors.Is(err, commerr.ErrRateLimited):
		return FailureCodeProviderRateLimited
	case errors.Is(err, commerr.ErrTimeout):
		return FailureCodeProviderTimeout
	case errors.Is(err, commerr.ErrValidationFailed):
		return FailureCodeProviderRejected
	case errors.Is(err, commerr.ErrParseFailed),
		errors.Is(err, provider.ErrResponseTooLarge):
		return FailureCodeProviderProtocol
	}

	return FailureCodeProviderUnavailable
}

func (s *Service) elapsedMS(startedAt time.Time) int64 {
	return s.now().Sub(startedAt).Milliseconds()
}

func applyResult(record *models.Evaluation, result policy.Result) {
	record.Outcome = result.Outcome.String()
	record.MatchedRuleID = result.MatchedRuleID
	record.ExecutionPath = result.Path.String()
}

// buildProviderRequest turns the compiled questions into a provider request,
// in the policy's deterministic question order.
func buildProviderRequest(
	compiled *policy.Compiled,
	input CreateInput,
) provider.Request {
	questions := make([]provider.Question, 0, len(compiled.QuestionIDs))

	for _, id := range compiled.QuestionIDs {
		question := compiled.Questions[id]
		questions = append(questions, provider.Question{
			ID:             question.ID,
			Type:           question.Type,
			Instructions:   question.Instructions,
			ChoiceCriteria: question.ChoiceCriteria,
			ScoreCriteria:  question.ScoreCriteria,
		})
	}

	return provider.Request{
		Model:     compiled.Model,
		State:     input.State,
		Questions: questions,
	}
}

func compileInline(
	document map[string]any,
	maxBytes, maxQuestions int,
) (*policy.Compiled, error) {
	encoded, err := encodeDocument(document)
	if err != nil {
		return nil, err
	}

	compiled, err := policy.CompileBytesWithMaxQuestions(
		encoded,
		maxBytes,
		maxQuestions,
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "compile the inline policy")
	}

	return compiled, nil
}
