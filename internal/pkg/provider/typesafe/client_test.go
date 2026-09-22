package typesafe

import (
	"context"
	"testing"
	"time"

	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	typesafeapi "github.com/psyb0t/vibecheck/pkg/gotypesafe"
	"github.com/psyb0t/vibecheck/pkg/gotypesafe/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEvaluateAdaptsTheCompiledPolicyToThePublicClient(t *testing.T) {
	t.Parallel()

	publicClient := mocks.NewMockClient(t)
	publicClient.EXPECT().Evaluate(mock.Anything, mock.MatchedBy(
		func(request typesafeapi.Request) bool {
			question := request.Questions["destructive"]

			return request.State == "remove a temporary directory" &&
				question.Type == typesafeapi.QuestionTypeNoul &&
				assert.ObjectsAreEqual(
					map[string]any{"true": "destroys state", "false": nil},
					question.Criteria,
				)
		},
	)).Return(typesafeapi.Response{
		Model: "jev-example",
		Answers: typesafeapi.Decisions{
			"destructive": {
				Type: typesafeapi.QuestionTypeNoul,
				Noul: 0.9,
			},
		},
		Usage: typesafeapi.TokenUsage{InputTokens: 12, OutputTokens: 1},
		Attempts: []typesafeapi.Attempt{{
			Number:     1,
			StatusCode: 200,
			Duration:   time.Millisecond,
		}},
	}, nil).Once()

	client := newWithClient(publicClient)
	response, err := client.Evaluate(context.Background(), provider.Request{
		State: "remove a temporary directory",
		Questions: []provider.Question{{
			ID:           "destructive",
			Type:         decision.QuestionTypeNoul,
			Instructions: "Does this destroy state?",
			NoulCriteria: map[string]any{
				"true":  "destroys state",
				"false": nil,
			},
		}},
	})
	require.NoError(t, err)

	assert.Equal(t, "jev-example", response.Model)
	assert.InDelta(t, 0.9, response.Answers["destructive"].Noul, 0.0001)
	assert.Equal(t, 12, response.Usage.InputTokens)
	assert.Equal(t, 1, response.AttemptCount())
}
