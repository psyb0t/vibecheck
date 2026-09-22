// Package server implements Vibecheck's REST surface over the generated
// strict handler interface.
//
// Handlers here are deliberately dumb. Each one collects transport input,
// shape-checks it, calls exactly one core service method, maps known errors
// onto the stable error envelope, and returns a generated response type. The
// core owns validation semantics, persistence, and every model to API
// conversion; nothing in this package imports a database model or a
// repository.
package server

import (
	"context"
	"net/http"

	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/core/evaluations"
	"github.com/psyb0t/vibecheck/internal/pkg/core/policies"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// Server implements api.StrictServerInterface.
type Server struct {
	evaluations *evaluations.Service
	policies    *policies.Service
}

// New builds the REST surface over the core services.
func New(
	evaluationService *evaluations.Service,
	policyService *policies.Service,
) *Server {
	return &Server{
		evaluations: evaluationService,
		policies:    policyService,
	}
}

// CreateEvaluation evaluates state against a policy.
func (s *Server) CreateEvaluation(
	ctx context.Context,
	request api.CreateEvaluationRequestObject,
) (api.CreateEvaluationResponseObject, error) {
	if request.Body == nil {
		return api.CreateEvaluation400JSONResponse{
			BadRequestJSONResponse: api.BadRequestJSONResponse(
				badRequest("a request body is required"),
			),
		}, nil
	}

	input := createInputFromRequest(ctx, request)

	result, err := s.evaluations.Create(ctx, input)
	if err != nil {
		return createEvaluationFailure(ctx, err), nil
	}

	if recorded, failed := classifyRecordedFailure(result); failed {
		return createEvaluationStatus(recorded), nil
	}

	return api.CreateEvaluation201JSONResponse(result), nil
}

// createEvaluationStatus returns the generated response object for an
// already-mapped failure.
func createEvaluationStatus(
	mapped failure,
) api.CreateEvaluationResponseObject {
	if response, ok := createEvaluationClientFailure(mapped); ok {
		return response
	}

	if response, ok := createEvaluationUpstreamFailure(mapped); ok {
		return response
	}

	return api.CreateEvaluation500JSONResponse{
		InternalServerErrorJSONResponse: api.InternalServerErrorJSONResponse(
			mapped.body,
		),
	}
}

// createInputFromRequest collects transport input into the core's own shape.
// It does no validation beyond reading what the request carried.
func createInputFromRequest(
	ctx context.Context,
	request api.CreateEvaluationRequestObject,
) evaluations.CreateInput {
	body := request.Body

	input := evaluations.CreateInput{
		State:     body.State,
		RequestID: requestIDFromContext(ctx),
	}

	if body.PolicyRef != nil {
		input.PolicyRef = &policy.Ref{
			Name:    body.PolicyRef.Name,
			Version: body.PolicyRef.Version,
		}
	}

	if body.InlinePolicy != nil {
		input.InlinePolicy = *body.InlinePolicy
	}

	if body.Facts != nil {
		input.Facts = *body.Facts
	}

	if body.Metadata != nil {
		input.Metadata = *body.Metadata
	}

	if request.Params.IdempotencyKey != nil {
		input.IdempotencyKey = request.Params.IdempotencyKey.String()
	}

	return input
}

// createEvaluationFailure maps a create failure onto the exact response the
// contract declares for that status.
func createEvaluationFailure(
	ctx context.Context,
	err error,
) api.CreateEvaluationResponseObject {
	mapped := classify(err)

	if response, ok := createEvaluationClientFailure(mapped); ok {
		return response
	}

	if response, ok := createEvaluationUpstreamFailure(mapped); ok {
		return response
	}

	// Anything unmapped is a defect rather than a caller mistake, so it is
	// the one branch that logs at error level.
	ctxscope.GetLogger(ctx).Error("create evaluation failed", "err", err)

	return api.CreateEvaluation500JSONResponse{
		InternalServerErrorJSONResponse: api.InternalServerErrorJSONResponse(
			mapped.body,
		),
	}
}

// createEvaluationClientFailure covers the statuses the caller can fix.
func createEvaluationClientFailure(
	mapped failure,
) (api.CreateEvaluationResponseObject, bool) {
	switch mapped.status {
	case http.StatusBadRequest, statusClientClosedRequest:
		return api.CreateEvaluation400JSONResponse{
			BadRequestJSONResponse: api.BadRequestJSONResponse(mapped.body),
		}, true
	case http.StatusNotFound:
		return api.CreateEvaluation404JSONResponse{
			NotFoundJSONResponse: api.NotFoundJSONResponse(mapped.body),
		}, true
	case http.StatusConflict:
		return api.CreateEvaluation409JSONResponse{
			ConflictJSONResponse: api.ConflictJSONResponse(mapped.body),
		}, true
	case http.StatusRequestEntityTooLarge:
		return api.CreateEvaluation413JSONResponse{
			PayloadTooLargeJSONResponse: api.PayloadTooLargeJSONResponse(
				mapped.body,
			),
		}, true
	case http.StatusUnprocessableEntity:
		body := api.UnprocessableEntityJSONResponse(mapped.body)

		return api.CreateEvaluation422JSONResponse{
			UnprocessableEntityJSONResponse: body,
		}, true
	case http.StatusTooManyRequests:
		return api.CreateEvaluation429JSONResponse{
			TooManyRequestsJSONResponse: api.TooManyRequestsJSONResponse(
				mapped.body,
			),
		}, true
	}

	return nil, false
}

// createEvaluationUpstreamFailure covers the statuses caused by the provider
// rather than by the request. Retrying these can succeed unchanged.
func createEvaluationUpstreamFailure(
	mapped failure,
) (api.CreateEvaluationResponseObject, bool) {
	switch mapped.status {
	case http.StatusBadGateway:
		return api.CreateEvaluation502JSONResponse{
			BadGatewayJSONResponse: api.BadGatewayJSONResponse(mapped.body),
		}, true
	case http.StatusServiceUnavailable:
		return api.CreateEvaluation503JSONResponse{
			ServiceUnavailableJSONResponse: api.ServiceUnavailableJSONResponse(
				mapped.body,
			),
		}, true
	case http.StatusGatewayTimeout:
		return api.CreateEvaluation504JSONResponse{
			GatewayTimeoutJSONResponse: api.GatewayTimeoutJSONResponse(
				mapped.body,
			),
		}, true
	}

	return nil, false
}

// ListEvaluations returns a page of recorded evaluations.
func (s *Server) ListEvaluations(
	ctx context.Context,
	request api.ListEvaluationsRequestObject,
) (api.ListEvaluationsResponseObject, error) {
	filter := evaluations.ListFilter{
		Limit:  api.DefaultListLimit,
		Offset: 0,
	}

	if request.Params.Limit != nil {
		filter.Limit = *request.Params.Limit
	}

	if request.Params.Offset != nil {
		filter.Offset = *request.Params.Offset
	}

	outOfRange := filter.Limit < 1 ||
		filter.Limit > api.MaxListLimit ||
		filter.Offset < 0
	if outOfRange {
		return api.ListEvaluations400JSONResponse{
			BadRequestJSONResponse: api.BadRequestJSONResponse(
				badRequest("limit and offset are outside the documented range"),
			),
		}, nil
	}

	applyListFilters(&filter, request.Params)

	page, err := s.evaluations.List(ctx, filter)
	if err != nil {
		ctxscope.GetLogger(ctx).Error("list evaluations failed", "err", err)

		body := api.InternalServerErrorJSONResponse(classify(err).body)

		return api.ListEvaluations500JSONResponse{
			InternalServerErrorJSONResponse: body,
		}, nil
	}

	return api.ListEvaluations200JSONResponse(page), nil
}

func applyListFilters(
	filter *evaluations.ListFilter,
	params api.ListEvaluationsParams,
) {
	if params.PolicyName != nil {
		filter.PolicyName = *params.PolicyName
	}

	if params.PolicyVersion != nil {
		filter.PolicyVersion = *params.PolicyVersion
	}

	if params.Outcome != nil {
		filter.Outcome = *params.Outcome
	}

	if params.Status != nil {
		filter.Status = string(*params.Status)
	}
}

// GetEvaluation returns one recorded evaluation.
func (s *Server) GetEvaluation(
	ctx context.Context,
	request api.GetEvaluationRequestObject,
) (api.GetEvaluationResponseObject, error) {
	result, err := s.evaluations.Get(ctx, request.EvaluationId)
	if err == nil {
		return api.GetEvaluation200JSONResponse(result), nil
	}

	mapped := classify(err)
	if mapped.status == http.StatusNotFound {
		return api.GetEvaluation404JSONResponse{
			NotFoundJSONResponse: api.NotFoundJSONResponse(mapped.body),
		}, nil
	}

	ctxscope.GetLogger(ctx).Error("get evaluation failed", "err", err)

	return api.GetEvaluation500JSONResponse{
		InternalServerErrorJSONResponse: api.InternalServerErrorJSONResponse(
			mapped.body,
		),
	}, nil
}

// SubmitFeedback records the outcome a human expected.
func (s *Server) SubmitFeedback(
	ctx context.Context,
	request api.SubmitFeedbackRequestObject,
) (api.SubmitFeedbackResponseObject, error) {
	if request.Body == nil {
		return api.SubmitFeedback400JSONResponse{
			BadRequestJSONResponse: api.BadRequestJSONResponse(
				badRequest("a request body is required"),
			),
		}, nil
	}

	input := evaluations.FeedbackInput{
		EvaluationID:    request.EvaluationId,
		ExpectedOutcome: request.Body.ExpectedOutcome,
	}

	if request.Body.Note != nil {
		input.Note = *request.Body.Note
	}

	result, err := s.evaluations.SubmitFeedback(ctx, input)
	if err == nil {
		return api.SubmitFeedback201JSONResponse(result), nil
	}

	mapped := classify(err)

	switch mapped.status {
	case http.StatusNotFound:
		return api.SubmitFeedback404JSONResponse{
			NotFoundJSONResponse: api.NotFoundJSONResponse(mapped.body),
		}, nil
	case http.StatusUnprocessableEntity:
		body := api.UnprocessableEntityJSONResponse(mapped.body)

		return api.SubmitFeedback422JSONResponse{
			UnprocessableEntityJSONResponse: body,
		}, nil
	}

	ctxscope.GetLogger(ctx).Error("submit feedback failed", "err", err)

	body := api.InternalServerErrorJSONResponse(mapped.body)

	return api.SubmitFeedback500JSONResponse{
		InternalServerErrorJSONResponse: body,
	}, nil
}

// ListPolicies returns every loaded policy.
func (s *Server) ListPolicies(
	ctx context.Context,
	_ api.ListPoliciesRequestObject,
) (api.ListPoliciesResponseObject, error) {
	result, err := s.policies.List(ctx)
	if err != nil {
		ctxscope.GetLogger(ctx).Error("list policies failed", "err", err)

		body := api.InternalServerErrorJSONResponse(classify(err).body)

		return api.ListPolicies500JSONResponse{
			InternalServerErrorJSONResponse: body,
		}, nil
	}

	return api.ListPolicies200JSONResponse(result), nil
}

// GetPolicy returns one loaded policy.
func (s *Server) GetPolicy(
	ctx context.Context,
	request api.GetPolicyRequestObject,
) (api.GetPolicyResponseObject, error) {
	result, err := s.policies.Get(
		ctx, request.PolicyName, request.PolicyVersion,
	)
	if err == nil {
		return api.GetPolicy200JSONResponse(result), nil
	}

	mapped := classify(err)
	if mapped.status == http.StatusNotFound {
		return api.GetPolicy404JSONResponse{
			NotFoundJSONResponse: api.NotFoundJSONResponse(mapped.body),
		}, nil
	}

	ctxscope.GetLogger(ctx).Error("get policy failed", "err", err)

	return api.GetPolicy500JSONResponse{
		InternalServerErrorJSONResponse: api.InternalServerErrorJSONResponse(
			mapped.body,
		),
	}, nil
}

// ValidatePolicy compiles a document without installing it.
func (s *Server) ValidatePolicy(
	ctx context.Context,
	request api.ValidatePolicyRequestObject,
) (api.ValidatePolicyResponseObject, error) {
	if request.Body == nil {
		return api.ValidatePolicy400JSONResponse{
			BadRequestJSONResponse: api.BadRequestJSONResponse(
				badRequest("a request body is required"),
			),
		}, nil
	}

	result, err := s.policies.Validate(ctx, request.Body.Policy)
	if err == nil {
		return api.ValidatePolicy200JSONResponse(result), nil
	}

	mapped := classify(err)

	switch mapped.status {
	case http.StatusRequestEntityTooLarge:
		return api.ValidatePolicy413JSONResponse{
			PayloadTooLargeJSONResponse: api.PayloadTooLargeJSONResponse(
				mapped.body,
			),
		}, nil
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return api.ValidatePolicy400JSONResponse{
			BadRequestJSONResponse: api.BadRequestJSONResponse(mapped.body),
		}, nil
	}

	ctxscope.GetLogger(ctx).Error("validate policy failed", "err", err)

	return api.ValidatePolicy500JSONResponse{
		InternalServerErrorJSONResponse: api.InternalServerErrorJSONResponse(
			mapped.body,
		),
	}, nil
}

// requestIDFromContext reads the ID the request-ID middleware seeded.
func requestIDFromContext(ctx context.Context) string {
	scope := ctxscope.Get(ctx)

	value, ok := scope[scopeKeyRequestID].(string)
	if !ok {
		return ""
	}

	return value
}
