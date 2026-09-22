package evaluations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"gorm.io/gorm"
)

// canonicalRequest is the shape an idempotency key is computed over. It
// includes the policy hash, so reusing a key against a policy that changed
// under the same version is a conflict rather than a silent replay of a
// decision the current rules would not make.
type canonicalRequest struct {
	PolicyHash string            `json:"policyHash"`
	State      any               `json:"state"`
	Facts      map[string]any    `json:"facts"`
	Metadata   map[string]string `json:"metadata"`
}

// canonicalRequestHash renders the request deterministically and hashes it.
// encoding/json sorts map keys, so two structurally identical requests hash
// the same regardless of the order the caller sent their fields in.
func canonicalRequestHash(
	policyHash string,
	input CreateInput,
) (string, error) {
	form := canonicalRequest{
		PolicyHash: policyHash,
		State:      input.State,
		Facts:      input.Facts,
		Metadata:   input.Metadata,
	}

	encoded, err := json.Marshal(form)
	if err != nil {
		return "", ctxerrors.Wrap(
			commerr.ErrMarshalFailed,
			"canonicalise the request for idempotency",
		)
	}

	sum := sha256.Sum256(encoded)

	return hex.EncodeToString(sum[:]), nil
}

// hashKey hashes the caller's idempotency key. The key itself is never
// stored: it is caller-chosen and could carry meaning the caller would not
// want at rest.
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))

	return hex.EncodeToString(sum[:])
}

// replay returns the original evaluation when this key has been seen with
// the same canonical request.
//
// A key seen with a different request is a conflict: silently returning the
// old answer would be wrong, and silently computing a new one would break
// the promise the key makes.
func (s *Service) replay(
	ctx context.Context,
	input CreateInput,
	requestHash string,
) (api.Evaluation, bool, error) {
	if input.IdempotencyKey == "" {
		return api.Evaluation{}, false, nil
	}

	repo := s.repo.IdempotencyRecord

	record, err := repo.WithContext(ctx).
		Where(repo.KeyHash.Eq(hashKey(input.IdempotencyKey))).
		First()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return api.Evaluation{}, false, nil
		}

		return api.Evaluation{}, false, ctxerrors.Wrap(
			err, "look up the idempotency record",
		)
	}

	if record.ExpiresAt.Before(s.now()) {
		return api.Evaluation{}, false, nil
	}

	if record.CanonicalRequestHash != requestHash {
		return api.Evaluation{}, false, ctxerrors.Wrap(
			ErrIdempotencyConflict, "idempotency key replay",
		)
	}

	evaluation, err := s.Get(ctx, record.EvaluationID)
	if err != nil {
		// The key points at a row that is gone, most likely retired by
		// retention cleanup. Treat the key as unused rather than failing
		// a request that can simply be evaluated again.
		if errors.Is(err, commerr.ErrNotFound) {
			return api.Evaluation{}, false, nil
		}

		return api.Evaluation{}, false, err
	}

	return evaluation, true, nil
}
