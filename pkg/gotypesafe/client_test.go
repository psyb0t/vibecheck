package typesafe_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/psyb0t/ctxerrors/commerr"
	typesafe "github.com/psyb0t/vibecheck/pkg/gotypesafe"
	"github.com/psyb0t/vibecheck/pkg/gotypesafe/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testAPIKey = "EXAMPLE-DO-NOT-USE"

const modelsResponse = `{
  "models": [{
    "name": "jev-latest",
    "description": "Current alias",
    "release_date": "2026-09-15"
  }]
}`

type recordedRequest struct {
	authorization string
	body          map[string]any
}

type testServer struct {
	t *testing.T

	mu       sync.Mutex
	statuses []int
	calls    []recordedRequest
}

func (s *testServer) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.URL.Path == "/v1/models" {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, err := writer.Write([]byte(modelsResponse))
		require.NoError(s.t, err)

		return
	}

	body := map[string]any{}
	require.NoError(s.t, json.NewDecoder(request.Body).Decode(&body))
	s.calls = append(s.calls, recordedRequest{
		authorization: request.Header.Get("Authorization"),
		body:          body,
	})

	status := http.StatusOK
	if len(s.statuses) > 0 {
		status = s.statuses[0]
		s.statuses = s.statuses[1:]
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	if status == http.StatusOK {
		_, err := writer.Write([]byte(successResponse))
		require.NoError(s.t, err)
	}
}

func (s *testServer) requests() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]recordedRequest(nil), s.calls...)
}

const successResponse = `{
  "model": "jev-1.13.0",
  "answers": {
    "destructive": {"type": "noul", "noul": 0.91},
    "action": {
      "type": "choice",
      "choice": "write",
      "confidence": 0.82,
      "probabilities": {"read": 0.1, "write": 0.9}
    },
    "blast_radius": {
      "type": "score",
      "score": 1.75,
      "confidence": 0.76,
      "legend": {
        "0": {"scope": "one record"},
        "1": ["one service"],
        "2": "many systems"
      },
      "probabilities": {"0": 0.05, "1": 0.15, "2": 0.8}
    }
  },
  "usage": {"input_tokens": 200, "output_tokens": 40}
}`

func newClient(
	t *testing.T,
	server http.Handler,
) *typesafe.HTTPClient {
	t.Helper()

	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)

	client, err := typesafe.New(typesafe.Config{
		BaseURL:     httpServer.URL,
		APIKey:      testAPIKey,
		MaxAttempts: 2,
		BackoffBase: time.Millisecond,
		BackoffMax:  time.Millisecond,
	})
	require.NoError(t, err)

	return client
}

func fullRequest() typesafe.Request {
	return typesafe.Request{
		State: map[string]any{
			"command": "remove a project-owned temporary resource",
		},
		Questions: map[string]typesafe.QuestionInput{
			"destructive": {
				Type: typesafe.QuestionTypeNoul,
				Instructions: map[string]any{
					"question": "Does this destroy state?",
				},
				Criteria: map[string]any{
					"true":  "state is destroyed",
					"false": "state is preserved",
				},
			},
			"action": {
				Type: typesafe.QuestionTypeChoice,
				Instructions: []any{
					"Classify the action",
					map[string]any{"mode": "strict"},
				},
				Criteria: map[string]any{
					"read":  nil,
					"write": map[string]any{"mutates": true},
				},
			},
			"blast_radius": {
				Type:         typesafe.QuestionTypeScore,
				Instructions: "How broad is the effect?",
				Criteria: []any{
					map[string]any{"scope": "one record"},
					[]any{"one service"},
					"many systems",
				},
			},
		},
	}
}

func TestEvaluateUsesOfficialStructuredContract(t *testing.T) {
	t.Parallel()

	server := &testServer{t: t}
	client := newClient(t, server)

	response, err := client.Evaluate(t.Context(), fullRequest())
	require.NoError(t, err)

	assert.Equal(t, "jev-1.13.0", response.Model)
	assert.Equal(t, 200, response.Usage.InputTokens)
	assert.Equal(t, 40, response.Usage.OutputTokens)
	assert.Equal(t, 1, response.AttemptCount())
	assert.InDelta(t, 0.91, response.Answers["destructive"].Noul, 0.0001)
	assert.Equal(t, "write", response.Answers["action"].Choice)
	assert.Equal(
		t,
		map[string]any{"scope": "one record"},
		response.Answers["blast_radius"].Legend["0"],
	)

	requests := server.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, "Bearer "+testAPIKey, requests[0].authorization)
	assert.Equal(t, typesafe.DefaultModel, requests[0].body["model"])

	questions, ok := requests[0].body["questions"].(map[string]any)
	require.True(t, ok)
	noul, ok := questions["destructive"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"true":  "state is destroyed",
		"false": "state is preserved",
	}, noul["criteria"])
}

func TestEvaluateRetriesOnlyRetryableStatuses(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name       string
		statuses   []int
		wantCalls  int
		wantErr    error
		wantResult bool
	}{
		{
			name:       "rate limit",
			statuses:   []int{http.StatusTooManyRequests, http.StatusOK},
			wantCalls:  2,
			wantResult: true,
		},
		{
			name:       "server failure",
			statuses:   []int{http.StatusBadGateway, http.StatusOK},
			wantCalls:  2,
			wantResult: true,
		},
		{
			name:      "bad request",
			statuses:  []int{http.StatusBadRequest},
			wantCalls: 1,
			wantErr:   commerr.ErrValidationFailed,
		},
		{
			name:      "unauthorized",
			statuses:  []int{http.StatusUnauthorized},
			wantCalls: 1,
			wantErr:   commerr.ErrNotAuthenticated,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := &testServer{t: t, statuses: tc.statuses}
			client := newClient(t, server)
			response, err := client.Evaluate(t.Context(), fullRequest())

			if tc.wantResult {
				require.NoError(t, err)
				assert.Equal(t, tc.wantCalls, response.AttemptCount())
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}

			assert.Len(t, server.requests(), tc.wantCalls)
		})
	}
}

func TestModelsUsesGeneratedModelsContract(t *testing.T) {
	t.Parallel()

	server := &testServer{t: t}
	client := newClient(t, server)

	models, err := client.Models(t.Context())
	require.NoError(t, err)
	require.Len(t, models, 1)
	assert.Equal(t, "jev-latest", models[0].Name)
}

func TestClientIsMockableWithGeneratedExpectations(t *testing.T) {
	t.Parallel()

	client := mocks.NewMockClient(t)
	request := typesafe.Request{
		State: "hello",
		Questions: map[string]typesafe.QuestionInput{
			"friendly": {Type: typesafe.QuestionTypeNoul},
		},
	}
	want := typesafe.Response{Model: "jev-example"}
	client.EXPECT().Evaluate(mock.Anything, request).Return(want, nil).Once()

	got, err := client.Evaluate(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
