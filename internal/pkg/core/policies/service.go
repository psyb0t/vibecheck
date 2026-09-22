// Package policies serves the compiled policy set to the transports.
//
// It is read-only by design. Policies are loaded, compiled, and hashed once
// at startup; there is no create, update, or delete, and validating a
// document never installs it. A policy change takes effect on restart, which
// is what keeps a running process from ever holding a half-reloaded rule set.
package policies

import (
	"context"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// Service exposes the loaded policy set and the compiler.
type Service struct {
	set *policy.Set

	// maxPolicyBytes bounds a document submitted for validation, matching
	// the ceiling a mounted file gets.
	maxPolicyBytes int
	maxQuestions   int
}

// New builds the service over an already-loaded set.
func New(set *policy.Set, maxPolicyBytes int) *Service {
	return NewWithMaxQuestions(set, maxPolicyBytes, policy.MaxQuestions)
}

// NewWithMaxQuestions builds the service with a deployment-specific question
// ceiling used by the validation endpoint.
func NewWithMaxQuestions(
	set *policy.Set,
	maxPolicyBytes, maxQuestions int,
) *Service {
	return &Service{
		set:            set,
		maxPolicyBytes: maxPolicyBytes,
		maxQuestions:   maxQuestions,
	}
}

// Compiled returns the compiled policy for a reference, for callers inside
// the core that need the policy itself rather than its API projection.
func (s *Service) Compiled(ref policy.Ref) (*policy.Compiled, error) {
	compiled, ok := s.set.Get(ref)
	if !ok {
		return nil, ctxerrors.Wrapf(
			commerr.ErrNotFound, "no policy %s is loaded", ref,
		)
	}

	return compiled, nil
}

// List returns every loaded policy, ordered by name then version.
func (s *Service) List(_ context.Context) (api.PolicyList, error) {
	all := s.set.All()

	items := make([]api.Policy, 0, len(all))
	for _, compiled := range all {
		items = append(items, CompiledToAPI(compiled))
	}

	return api.PolicyList{Items: items}, nil
}

// Get returns one loaded policy.
func (s *Service) Get(
	_ context.Context,
	name, version string,
) (api.Policy, error) {
	compiled, err := s.Compiled(policy.Ref{Name: name, Version: version})
	if err != nil {
		return api.Policy{}, err
	}

	return CompiledToAPI(compiled), nil
}

// Validate compiles a document and reports the result.
//
// A document that fails to compile is a successful validation call with
// valid=false, not an error: the caller asked whether it compiles and got a
// truthful answer. Only a failure to even attempt the compile is an error.
func (s *Service) Validate(
	_ context.Context,
	document map[string]any,
) (api.PolicyValidation, error) {
	if len(document) == 0 {
		return api.PolicyValidation{}, ctxerrors.Wrap(
			commerr.ErrValidationFailed, "no policy document was supplied",
		)
	}

	encoded, err := encodeDocument(document)
	if err != nil {
		return api.PolicyValidation{}, err
	}

	compiled, compileErr := policy.CompileBytesWithMaxQuestions(
		encoded,
		s.maxPolicyBytes,
		s.maxQuestions,
	)
	if compileErr != nil {
		message := compileErr.Error()

		//nolint:nilerr // Validation answering "this policy does not compile"
		// is a successful call with a negative verdict. The compile error is
		// the payload, so returning it as a Go error would turn a 200 into a
		// 500 and hide the reason the caller asked for.
		return api.PolicyValidation{Valid: false, Error: &message}, nil
	}

	projected := CompiledToAPI(compiled)

	return api.PolicyValidation{Valid: true, Policy: &projected}, nil
}
