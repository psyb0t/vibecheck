package evaluations

import (
	"encoding/json"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// validateRequestShape enforces the transport-independent ceilings on
// caller-supplied maps.
//
// These are separate from the policy's own input contract: a policy decides
// which facts it needs, this decides how much a caller may send at all. Both
// have to hold, and this one runs first so an oversized request is rejected
// before any policy work happens.
func (s *Service) validateRequestShape(input CreateInput) error {
	if err := s.validateStateSize(input.State); err != nil {
		return err
	}

	if err := s.validateFactCount(input.Facts); err != nil {
		return err
	}

	return s.validateMetadata(input.Metadata)
}

func (s *Service) validateStateSize(state any) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return ctxerrors.Wrap(
			commerr.ErrValidationFailed,
			"state must be JSON serializable",
		)
	}

	if s.config.MaxStateBytes > 0 && len(encoded) > s.config.MaxStateBytes {
		return ctxerrors.Wrapf(
			commerr.ErrValidationFailed,
			"state is %d bytes, limit is %d",
			len(encoded), s.config.MaxStateBytes,
		)
	}

	return nil
}

func (s *Service) validateFactCount(facts decision.Facts) error {
	if s.config.MaxFacts > 0 && len(facts) > s.config.MaxFacts {
		return ctxerrors.Wrapf(
			commerr.ErrValidationFailed,
			"at most %d facts are accepted, got %d",
			s.config.MaxFacts, len(facts),
		)
	}

	for _, name := range sortedKeys(facts) {
		if !decision.IsFactName(name) {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"fact name %q is not a valid dotted identifier", name,
			)
		}

		if !isScalarFact(facts[name]) {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"fact %q must be a string, boolean, or number", name,
			)
		}
	}

	return nil
}

// isScalarFact rejects nested objects and arrays. Facts are what
// deterministic rules compare against, and a rule can only compare scalars.
func isScalarFact(value any) bool {
	switch value.(type) {
	case string, bool:
		return true
	}

	_, isNumber := decision.AsNumber(value)

	return isNumber
}

func (s *Service) validateMetadata(metadata map[string]string) error {
	if s.config.MaxMetadataEntries > 0 &&
		len(metadata) > s.config.MaxMetadataEntries {
		return ctxerrors.Wrapf(
			commerr.ErrValidationFailed,
			"at most %d metadata entries are accepted, got %d",
			s.config.MaxMetadataEntries, len(metadata),
		)
	}

	for _, key := range sortedKeys(metadata) {
		if !decision.IsFactName(key) {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"metadata key %q is not a valid identifier", key,
			)
		}

		if s.config.MaxMetadataValueLength > 0 &&
			len(metadata[key]) > s.config.MaxMetadataValueLength {
			return ctxerrors.Wrapf(
				commerr.ErrValidationFailed,
				"metadata value for %q exceeds %d characters",
				key, s.config.MaxMetadataValueLength,
			)
		}
	}

	return nil
}
