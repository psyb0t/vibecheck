//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MCP paths. Both forms must reach the handler; see the no-redirect test.
const (
	mcpPath      = "/mcp"
	mcpSlashPath = "/mcp/"
)

const (
	jsonRPCVersion  = "2.0"
	mcpAcceptHeader = "application/json, text/event-stream"
	mcpProtocol     = "2025-06-18"
)

// Tool names the server advertises.
const (
	toolEvaluate       = "vibecheck_evaluate"
	toolListPolicies   = "vibecheck_list_policies"
	toolGetPolicy      = "vibecheck_get_policy"
	toolGetEvaluation  = "vibecheck_get_evaluation"
	toolValidatePolicy = "vibecheck_validate_policy"
	toolSubmitFeedback = "vibecheck_submit_feedback"
)

// rpcResponse is a decoded JSON-RPC reply.
type rpcResponse struct {
	Status int
	Result map[string]any
	Error  *rpcError
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpc issues one JSON-RPC call against an MCP path.
func (c *client) rpc(
	t *testing.T,
	path, method string,
	params any,
) rpcResponse {
	t.Helper()

	body := map[string]any{
		"jsonrpc": jsonRPCVersion,
		"id":      1,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}

	res := c.do(t, http.MethodPost, path, body, map[string]string{
		"Accept": mcpAcceptHeader,
	})

	decoded := rpcResponse{Status: res.Status}

	if res.Status != http.StatusOK {
		return decoded
	}

	var envelope struct {
		Result map[string]any `json:"result"`
		Error  *rpcError      `json:"error"`
	}

	require.NoError(
		t, json.Unmarshal(res.Body, &envelope),
		"MCP replies are JSON: %s", string(res.Body),
	)

	decoded.Result = envelope.Result
	decoded.Error = envelope.Error

	return decoded
}

// callTool invokes one tool and returns its structured output.
func (c *client) callTool(
	t *testing.T,
	name string,
	arguments map[string]any,
) (map[string]any, bool) {
	t.Helper()

	reply := c.rpc(t, mcpPath, "tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})

	require.Equal(t, http.StatusOK, reply.Status)
	require.Nil(t, reply.Error, "tool calls are not protocol errors")
	require.NotNil(t, reply.Result)

	isError, _ := reply.Result["isError"].(bool)

	structured, _ := reply.Result["structuredContent"].(map[string]any)

	return structured, isError
}

// The whole point of registering both exact patterns: net/http would answer
// a POST to "/mcp" with a 301 to "/mcp/" if only the subtree were
// registered, and a redirected POST loses its body. Neither form may
// redirect, and both must reach the handler.
func TestMCPIsReachableAtBothPathsWithoutRedirecting(t *testing.T) {
	api := newClient(sharedApp)

	for _, path := range []string{mcpPath, mcpSlashPath} {
		t.Run("POST "+path, func(t *testing.T) {
			body := map[string]any{
				"jsonrpc": jsonRPCVersion,
				"id":      1,
				"method":  "tools/list",
			}

			res := api.do(t, http.MethodPost, path, body, map[string]string{
				"Accept": mcpAcceptHeader,
			})

			require.Equal(
				t, http.StatusOK, res.Status,
				"%s must be handled directly, body was %s",
				path, string(res.Body),
			)

			assert.Empty(
				t, res.Header.Get("Location"),
				"%s must not answer with a redirect", path,
			)

			var envelope struct {
				Result struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				} `json:"result"`
			}
			require.NoError(t, json.Unmarshal(res.Body, &envelope))
			assert.NotEmpty(
				t, envelope.Result.Tools,
				"%s reached the MCP handler and listed tools", path,
			)
		})
	}
}

func TestMCPAdvertisesEveryTool(t *testing.T) {
	api := newClient(sharedApp)

	reply := api.rpc(t, mcpPath, "tools/list", nil)
	require.Equal(t, http.StatusOK, reply.Status)
	require.Nil(t, reply.Error)

	rawTools, ok := reply.Result["tools"].([]any)
	require.True(t, ok, "tools/list returns a tools array")

	names := make(map[string]bool, len(rawTools))

	for _, entry := range rawTools {
		tool, ok := entry.(map[string]any)
		require.True(t, ok)

		name, ok := tool["name"].(string)
		require.True(t, ok)
		names[name] = true

		assert.NotEmpty(
			t, tool["description"],
			"tool %s needs a description a model can read", name,
		)
		assert.NotNil(
			t, tool["inputSchema"],
			"tool %s needs an input schema", name,
		)
	}

	for _, want := range []string{
		toolEvaluate, toolListPolicies, toolGetPolicy,
		toolGetEvaluation, toolValidatePolicy, toolSubmitFeedback,
	} {
		assert.True(t, names[want], "tool %s must be advertised", want)
	}
}

func TestMCPInitializeReportsServerIdentity(t *testing.T) {
	api := newClient(sharedApp)

	reply := api.rpc(t, mcpPath, "initialize", map[string]any{
		"protocolVersion": mcpProtocol,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name": "vibecheck-tests", "version": "1",
		},
	})

	require.Equal(t, http.StatusOK, reply.Status)
	require.Nil(t, reply.Error)

	info, ok := reply.Result["serverInfo"].(map[string]any)
	require.True(t, ok, "initialize reports serverInfo")
	assert.Equal(t, "vibecheck", info["name"])
	assert.NotEmpty(t, info["version"])
}

// Both transports must be the same service, not two implementations that
// happen to agree today.
func TestMCPAndRESTReturnEquivalentResults(t *testing.T) {
	api := newClient(sharedApp)

	t.Run("policy listing", func(t *testing.T) {
		viaMCP, isError := api.callTool(t, toolListPolicies, nil)
		require.False(t, isError)

		res := api.do(t, http.MethodGet, "/v1/policies", nil, nil)
		require.Equal(t, http.StatusOK, res.Status)

		assert.Equal(
			t, res.Map(t), viaMCP,
			"MCP and REST read the same policy service",
		)
	})

	t.Run("an evaluation is readable through both", func(t *testing.T) {
		created, isError := api.callTool(t, toolEvaluate, map[string]any{
			"policyName":    policyName,
			"policyVersion": policyVersion,
			"state":         "cross-transport fixture",
			"facts":         allowingFacts(),
		})
		require.False(t, isError, "the evaluation tool should succeed")

		id, ok := created["id"].(string)
		require.True(t, ok, "the tool returns an evaluation id")

		res := api.do(t, http.MethodGet, "/v1/evaluations/"+id, nil, nil)
		require.Equal(t, http.StatusOK, res.Status)

		assert.Equal(
			t, res.Map(t), created,
			"an MCP evaluation is the same audit row REST serves",
		)
	})
}

func TestMCPEvaluateHonoursThePreRuleFloor(t *testing.T) {
	api := newClient(sharedApp)

	before := sharedProvider.CallCount()

	result, isError := api.callTool(t, toolEvaluate, map[string]any{
		"policyName":    policyName,
		"policyVersion": policyVersion,
		"state":         "delete an unowned path over MCP",
		"facts":         blockingFacts(),
	})
	require.False(t, isError)

	assert.Equal(t, "block", result["outcome"])
	assert.Equal(t, "pre_rule", result["executionPath"])
	assert.Equal(
		t, before, sharedProvider.CallCount(),
		"the deterministic floor applies to MCP exactly as it does to REST",
	)
}

func TestMCPToolsReportFailuresAsToolErrors(t *testing.T) {
	api := newClient(sharedApp)

	testCases := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{
			name: "an unknown policy",
			tool: toolEvaluate,
			arguments: map[string]any{
				"policyName":    "no-such-policy",
				"policyVersion": "1.0.0",
				"state":         "anything",
				"facts":         allowingFacts(),
			},
		},
		{
			name: "a missing required fact",
			tool: toolEvaluate,
			arguments: map[string]any{
				"policyName":    policyName,
				"policyVersion": policyVersion,
				"state":         "anything",
				"facts":         map[string]any{"action.kind": "edit_file"},
			},
		},
		{
			name: "an evaluation id that is not a uuid",
			tool: toolGetEvaluation,
			arguments: map[string]any{
				"evaluationId": "definitely-not-a-uuid",
			},
		},
		{
			name: "an unknown evaluation",
			tool: toolGetEvaluation,
			arguments: map[string]any{
				"evaluationId": uuid.NewString(),
			},
		},
		{
			name: "an unknown policy by ref",
			tool: toolGetPolicy,
			arguments: map[string]any{
				"policyName": "nope", "policyVersion": "1.0.0",
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, isError := api.callTool(
				t, testCase.tool, testCase.arguments,
			)
			assert.True(
				t, isError,
				"a failing tool reports isError so a model can react",
			)
		})
	}
}

func TestMCPValidatePolicyDoesNotInstall(t *testing.T) {
	// One policy is loaded on the shared app, which is the count this
	// asserts validation does not change.
	api := newClient(sharedApp)

	candidate := map[string]any{
		"apiVersion": "vibecheck.psyb0t.dev/v1alpha1",
		"kind":       "DecisionPolicy",
		"metadata": map[string]any{
			"name": "mcp-candidate", "version": "0.1.0",
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

	report, isError := api.callTool(t, toolValidatePolicy, map[string]any{
		"policy": candidate,
	})
	require.False(t, isError)
	assert.Equal(t, true, report["valid"])

	listed, isError := api.callTool(t, toolListPolicies, nil)
	require.False(t, isError)

	items, ok := listed["items"].([]any)
	require.True(t, ok)
	assert.Len(
		t, items, 1,
		"validating over MCP must not add the document to the loaded set",
	)
}

func TestMCPSubmitFeedbackRecordsAgainstTheEvaluation(t *testing.T) {
	api := newClient(sharedApp)

	created, isError := api.callTool(t, toolEvaluate, map[string]any{
		"policyName":    policyName,
		"policyVersion": policyVersion,
		"state":         "mcp feedback fixture",
		"facts":         allowingFacts(),
	})
	require.False(t, isError)

	id, ok := created["id"].(string)
	require.True(t, ok)

	feedback, isError := api.callTool(t, toolSubmitFeedback, map[string]any{
		"evaluationId":    id,
		"expectedOutcome": "review",
		"note":            "recorded over MCP",
	})
	require.False(t, isError)

	assert.Equal(t, id, feedback["evaluationId"])
	assert.Equal(t, "review", feedback["expectedOutcome"])
}

func TestMCPRejectsMalformedJSONRPC(t *testing.T) {
	api := newClient(sharedApp)

	res := api.doRaw(t, http.MethodPost, mcpPath,
		[]byte("{not json"), map[string]string{"Accept": mcpAcceptHeader})

	assert.GreaterOrEqual(t, res.Status, http.StatusBadRequest)
	assert.Less(t, res.Status, http.StatusInternalServerError,
		"a malformed JSON-RPC frame is the caller's fault")
}
