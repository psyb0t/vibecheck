package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
)

const (
	patternCreateEvaluation = http.MethodPost + " " + routeEvaluations
	patternSubmitFeedback   = http.MethodPost + " " + routeEvaluationFeedbck
	patternValidatePolicy   = http.MethodPost + " " + routePolicyValidate
)

// strictJSONBodies rejects unknown fields and concatenated JSON values before
// the generated binder decodes the same bytes into its request type.
func (r *Router) strictJSONBodies() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			target := strictJSONTarget(req.Pattern)
			if target == nil {
				next.ServeHTTP(w, req)

				return
			}

			body, err := io.ReadAll(req.Body)
			if err != nil {
				r.writeRequestError(
					w,
					req,
					ctxerrors.Wrap(err, "read the body for strict decoding"),
				)

				return
			}

			if err := decodeSingleStrictJSON(body, target); err != nil {
				r.writeRequestError(w, req, err)

				return
			}

			req.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, req)
		})
	}
}

func strictJSONTarget(pattern string) any {
	switch pattern {
	case patternCreateEvaluation:
		return &api.CreateEvaluationJSONRequestBody{}
	case patternSubmitFeedback:
		return &api.SubmitFeedbackJSONRequestBody{}
	case patternValidatePolicy:
		return &api.ValidatePolicyJSONRequestBody{}
	default:
		return nil
	}
}

func decodeSingleStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return ctxerrors.Wrap(err, "decode strict request body")
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ctxerrors.Wrap(
				commerr.ErrParseFailed,
				"request body contains more than one JSON value",
			)
		}

		return ctxerrors.Wrap(err, "decode trailing request body data")
	}

	return nil
}
