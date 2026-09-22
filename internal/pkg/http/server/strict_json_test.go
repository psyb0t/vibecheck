package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStrictJSONBodiesRejectsInvalidRequestShapes(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		pattern string
		body    string
		want    int
	}{
		{
			name:    "known body",
			pattern: patternValidatePolicy,
			body:    `{"policy":{}}`,
			want:    http.StatusNoContent,
		},
		{
			name:    "unknown top-level field",
			pattern: patternValidatePolicy,
			body:    `{"policy":{},"typo":true}`,
			want:    http.StatusBadRequest,
		},
		{
			name:    "concatenated values",
			pattern: patternValidatePolicy,
			body:    `{"policy":{}} {"policy":{}}`,
			want:    http.StatusBadRequest,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			router := &Router{}
			handler := router.strictJSONBodies()(http.HandlerFunc(func(
				w http.ResponseWriter,
				_ *http.Request,
			) {
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				routePolicyValidate,
				strings.NewReader(tc.body),
			)
			req.Pattern = tc.pattern
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, req)

			assert.Equal(t, tc.want, recorder.Code)
		})
	}
}

func TestStrictJSONBodiesLeavesBodyRoutesAlone(t *testing.T) {
	t.Parallel()

	router := &Router{}
	handler := router.strictJSONBodies()(http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		routePolicies,
		strings.NewReader(`{"unknown":true}`),
	)
	req.Pattern = http.MethodGet + " " + routePolicies
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}
