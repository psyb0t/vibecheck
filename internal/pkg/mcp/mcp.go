// Package mcp exposes Vibecheck over the Model Context Protocol.
//
// It is a transport, nothing more. Every tool here converts its typed input
// into the same core service call the REST handlers make, and returns the
// same generated API types. No rule evaluation, no persistence, and no
// provider code lives in this package, which is what makes REST and MCP
// produce identical decisions and identical audit rows for one request.
package mcp

import (
	"context"
	"net/http"
	"reflect"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/core/evaluations"
	"github.com/psyb0t/vibecheck/internal/pkg/core/policies"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// Server identity reported during initialize.
const (
	ServerName  = "vibecheck"
	ServerTitle = "Vibecheck"
)

// Tool names. They are the protocol's public surface, so they get constants
// rather than being spelled out at each registration.
const (
	ToolEvaluate       = "vibecheck_evaluate"
	ToolListPolicies   = "vibecheck_list_policies"
	ToolGetPolicy      = "vibecheck_get_policy"
	ToolGetEvaluation  = "vibecheck_get_evaluation"
	ToolValidatePolicy = "vibecheck_validate_policy"
	ToolSubmitFeedback = "vibecheck_submit_feedback"
)

const instructions = "Evaluate state against a versioned Vibecheck policy " +
	"and read the resulting decisions. Vibecheck is advisory: it returns an " +
	"outcome and never performs the action it judges."

// outputSchemaFor derives a tool's output schema, describing every UUID as a
// string.
//
// The generated API types carry IDs as uuid.UUID, which is [16]byte. Schema
// inference therefore derives "array" while the value marshals to a string
// through the type's own MarshalJSON. Left uncorrected, the SDK validates
// every result carrying an ID against a schema it can never satisfy and
// turns a successful evaluation into a tool failure.
func outputSchemaFor[T any]() (*jsonschema.Schema, error) {
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{
			reflect.TypeFor[uuid.UUID](): {
				Type:   "string",
				Format: "uuid",
			},
		},
	})
	if err != nil {
		return nil, ctxerrors.Wrap(err, "derive the tool output schema")
	}

	return schema, nil
}

// Services is the core surface the MCP tools call. They are the very same
// service values the REST handlers hold, which is what makes the two
// transports produce identical decisions and identical audit rows.
type Services struct {
	Evaluations *evaluations.Service
	Policies    *policies.Service
}

// HandlerConfig carries the transport limits the deployment configured.
type HandlerConfig struct {
	// MaxRequestBytes bounds a single JSON-RPC request body.
	MaxRequestBytes int64
}

// Handler builds the Streamable HTTP handler to mount at /mcp.
//
// The transport runs stateless. Each POST is a complete request/response
// exchange, which matches Vibecheck's synchronous API and, critically, means
// the context values the outer HTTP middleware sets reach every tool call.
// In the session-based mode only the session-establishing request's context
// propagates, so a per-request identifier set by middleware would be wrong
// for every later call on that session.
func Handler(
	services Services,
	recorder *metrics.Metrics,
	config HandlerConfig,
) (http.Handler, error) {
	server, err := NewServer(services, recorder)
	if err != nil {
		return nil, err
	}

	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		&mcpsdk.StreamableHTTPOptions{
			Stateless:    true,
			JSONResponse: true,

			MaxRequestBodyBytes: config.MaxRequestBytes,

			// A client that hangs up mid-evaluation should cancel the
			// provider call it is paying for, not leave it running.
			PropagateRequestCancellation: true,
		},
	), nil
}

// NewServer builds the MCP server and registers every tool.
//
// It fails when a tool's output schema cannot be derived. That depends only
// on the generated types, so it is a build-time fault surfaced at startup
// rather than on the first call.
func NewServer(
	services Services,
	recorder *metrics.Metrics,
) (*mcpsdk.Server, error) {
	server := mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:    ServerName,
			Title:   ServerTitle,
			Version: api.SpecVersion,
		},
		&mcpsdk.ServerOptions{Instructions: instructions},
	)

	registry := &toolRegistry{services: services, metrics: recorder}
	if err := registry.register(server); err != nil {
		return nil, err
	}

	return server, nil
}

// toolRegistry holds what every tool needs and keeps the registration list in
// one place.
type toolRegistry struct {
	services Services
	metrics  *metrics.Metrics
}

func (r *toolRegistry) register(server *mcpsdk.Server) error {
	evaluationSchema, err := outputSchemaFor[api.Evaluation]()
	if err != nil {
		return err
	}

	feedbackSchema, err := outputSchemaFor[api.Feedback]()
	if err != nil {
		return err
	}

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:         ToolEvaluate,
		Description:  "Evaluate state against a loaded or inline policy.",
		OutputSchema: evaluationSchema,
	}, instrument(r.metrics, ToolEvaluate, r.evaluate))

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        ToolListPolicies,
		Description: "List every policy this deployment loaded at startup.",
	}, instrument(r.metrics, ToolListPolicies, r.listPolicies))

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        ToolGetPolicy,
		Description: "Fetch one loaded policy by name and version.",
	}, instrument(r.metrics, ToolGetPolicy, r.getPolicy))

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:         ToolGetEvaluation,
		Description:  "Fetch one recorded evaluation by ID.",
		OutputSchema: evaluationSchema,
	}, instrument(r.metrics, ToolGetEvaluation, r.getEvaluation))

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: ToolValidatePolicy,
		Description: "Compile a policy document and report whether it is " +
			"valid. Nothing is installed.",
	}, instrument(r.metrics, ToolValidatePolicy, r.validatePolicy))

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:         ToolSubmitFeedback,
		Description:  "Record the outcome a human expected for an evaluation.",
		OutputSchema: feedbackSchema,
	}, instrument(r.metrics, ToolSubmitFeedback, r.submitFeedback))

	return nil
}

// instrument wraps a tool handler with metrics and timing. A tool error is
// still a completed call at the protocol level, so it is counted separately
// rather than being dropped.
func instrument[In, Out any](
	recorder *metrics.Metrics,
	name string,
	handler mcpsdk.ToolHandlerFor[In, Out],
) mcpsdk.ToolHandlerFor[In, Out] {
	return func(
		ctx context.Context,
		request *mcpsdk.CallToolRequest,
		input In,
	) (*mcpsdk.CallToolResult, Out, error) {
		if recorder != nil {
			recorder.MCPInFlight().Inc()
			defer recorder.MCPInFlight().Dec()
		}

		startedAt := time.Now()

		result, output, err := handler(ctx, request, input)

		if recorder != nil {
			class := metrics.OutcomeClassSuccess
			if err != nil {
				class = metrics.OutcomeClassError
			}

			recorder.ObserveMCPTool(
				name, class, time.Since(startedAt).Seconds(),
			)
		}

		return result, output, err
	}
}

// EvaluateInput is the typed input for the evaluate tool.
type EvaluateInput struct {
	PolicyName    string `json:"policyName,omitempty"    jsonschema:"name of a loaded policy; required unless inlinePolicy is supplied"` //nolint:lll // jsonschema description text cannot be split
	PolicyVersion string `json:"policyVersion,omitempty" jsonschema:"version of the loaded policy"`                                      //nolint:lll // jsonschema description text cannot be split

	InlinePolicy map[string]any `json:"inlinePolicy,omitempty" jsonschema:"a complete policy document, refused unless the deployment enables inline policies"` //nolint:lll // jsonschema description text cannot be split

	State any `json:"state" jsonschema:"the content to judge, a string or a structured value"` //nolint:lll // jsonschema description text cannot be split

	Facts    map[string]any    `json:"facts,omitempty"    jsonschema:"trusted scalar values the deterministic rules read"`   //nolint:lll // jsonschema description text cannot be split
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"bounded caller metadata recorded with the evaluation"` //nolint:lll // jsonschema description text cannot be split

	IdempotencyKey string `json:"idempotencyKey,omitempty" jsonschema:"a caller-chosen UUID that makes this request replayable"` //nolint:lll // jsonschema description text cannot be split
}

func (r *toolRegistry) evaluate(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input EvaluateInput,
) (*mcpsdk.CallToolResult, api.Evaluation, error) {
	createInput := evaluations.CreateInput{
		State:          input.State,
		Facts:          input.Facts,
		Metadata:       input.Metadata,
		IdempotencyKey: input.IdempotencyKey,
		RequestID:      requestIDFromContext(ctx),
		InlinePolicy:   input.InlinePolicy,
	}

	if input.PolicyName != "" || input.PolicyVersion != "" {
		createInput.PolicyRef = &policy.Ref{
			Name:    input.PolicyName,
			Version: input.PolicyVersion,
		}
	}

	result, err := r.services.Evaluations.Create(ctx, createInput)
	if err != nil {
		return nil, api.Evaluation{}, toolError(ctx, "evaluate", err)
	}

	return nil, result, nil
}

// ListPoliciesInput is empty: listing takes no arguments.
type ListPoliciesInput struct{}

func (r *toolRegistry) listPolicies(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	_ ListPoliciesInput,
) (*mcpsdk.CallToolResult, api.PolicyList, error) {
	result, err := r.services.Policies.List(ctx)
	if err != nil {
		return nil, api.PolicyList{}, toolError(ctx, "list policies", err)
	}

	return nil, result, nil
}

// GetPolicyInput selects one loaded policy.
type GetPolicyInput struct {
	PolicyName    string `json:"policyName"    jsonschema:"name of the loaded policy"`    //nolint:lll // jsonschema description text cannot be split
	PolicyVersion string `json:"policyVersion" jsonschema:"version of the loaded policy"` //nolint:lll // jsonschema description text cannot be split
}

func (r *toolRegistry) getPolicy(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input GetPolicyInput,
) (*mcpsdk.CallToolResult, api.Policy, error) {
	result, err := r.services.Policies.Get(
		ctx, input.PolicyName, input.PolicyVersion,
	)
	if err != nil {
		return nil, api.Policy{}, toolError(ctx, "get policy", err)
	}

	return nil, result, nil
}

// GetEvaluationInput selects one recorded evaluation.
type GetEvaluationInput struct {
	EvaluationID string `json:"evaluationId" jsonschema:"the evaluation UUID"`
}

func (r *toolRegistry) getEvaluation(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input GetEvaluationInput,
) (*mcpsdk.CallToolResult, api.Evaluation, error) {
	id, err := uuid.Parse(input.EvaluationID)
	if err != nil {
		return nil, api.Evaluation{}, ctxerrors.Wrap(
			err, "evaluationId must be a UUID",
		)
	}

	result, err := r.services.Evaluations.Get(ctx, id)
	if err != nil {
		return nil, api.Evaluation{}, toolError(ctx, "get evaluation", err)
	}

	return nil, result, nil
}

// ValidatePolicyInput carries a document to compile.
type ValidatePolicyInput struct {
	Policy map[string]any `json:"policy" jsonschema:"the policy document to compile"` //nolint:lll // jsonschema description text cannot be split
}

func (r *toolRegistry) validatePolicy(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input ValidatePolicyInput,
) (*mcpsdk.CallToolResult, api.PolicyValidation, error) {
	result, err := r.services.Policies.Validate(ctx, input.Policy)
	if err != nil {
		return nil, api.PolicyValidation{}, toolError(
			ctx, "validate policy", err,
		)
	}

	return nil, result, nil
}

// SubmitFeedbackInput labels a past decision.
type SubmitFeedbackInput struct {
	EvaluationID    string `json:"evaluationId"    jsonschema:"the evaluation UUID"`          //nolint:lll // jsonschema description text cannot be split
	ExpectedOutcome string `json:"expectedOutcome" jsonschema:"the outcome a human expected"` //nolint:lll // jsonschema description text cannot be split
	Note            string `json:"note,omitempty"  jsonschema:"an optional bounded note"`     //nolint:lll // jsonschema description text cannot be split
}

func (r *toolRegistry) submitFeedback(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	input SubmitFeedbackInput,
) (*mcpsdk.CallToolResult, api.Feedback, error) {
	id, err := uuid.Parse(input.EvaluationID)
	if err != nil {
		return nil, api.Feedback{}, ctxerrors.Wrap(
			err, "evaluationId must be a UUID",
		)
	}

	result, err := r.services.Evaluations.SubmitFeedback(
		ctx,
		evaluations.FeedbackInput{
			EvaluationID:    id,
			ExpectedOutcome: input.ExpectedOutcome,
			Note:            input.Note,
		},
	)
	if err != nil {
		return nil, api.Feedback{}, toolError(ctx, "submit feedback", err)
	}

	return nil, result, nil
}

// toolError logs the real failure and returns the error the client sees.
//
// The SDK turns a returned error into an isError tool result, which is what a
// model can read and react to. The message deliberately stays the core's own
// text: it never contains a provider response body, because the core never
// puts one there.
func toolError(ctx context.Context, operation string, err error) error {
	ctxscope.GetLogger(ctx).Error(
		"mcp tool failed", "operation", operation, "err", err,
	)

	return ctxerrors.Wrapf(err, "%s failed", operation)
}

// requestIDFromContext reads the correlation ID the HTTP middleware seeded.
func requestIDFromContext(ctx context.Context) string {
	scope := ctxscope.Get(ctx)

	value, ok := scope["request_id"].(string)
	if !ok {
		return ""
	}

	return value
}
