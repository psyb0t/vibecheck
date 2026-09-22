package evaluations

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/db/models"
	"github.com/psyb0t/vibecheck/internal/pkg/db/repositories"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ensureSnapshot records the exact policy this evaluation will point at.
//
// The hash is the primary key, so an unchanged policy reuses its row across
// restarts and every historical evaluation keeps resolving to the bytes that
// actually decided it. An inline policy gets a snapshot too, which is what
// makes a one-shot decision as reconstructable as a named one.
func (s *Service) ensureSnapshot(
	ctx context.Context,
	compiled *policy.Compiled,
) error {
	snapshot := &models.PolicySnapshot{
		Hash:          compiled.Hash,
		Name:          compiled.Ref.Name,
		Version:       compiled.Ref.Version,
		CanonicalJSON: string(compiled.Canonical),
		LoadedAt:      s.now(),
	}

	repo := s.repo.PolicySnapshot

	if err := repo.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(snapshot); err != nil {
		return ctxerrors.Wrap(err, "record the policy snapshot")
	}

	return nil
}

// persist writes the evaluation and, when the caller supplied one, its
// idempotency key. Both land in one transaction so a replay can never find a
// key pointing at a row that was never written.
func (s *Service) persist(
	ctx context.Context,
	record *models.Evaluation,
	input CreateInput,
	requestHash string,
) error {
	sealed, err := s.sealInput(input)
	if err != nil {
		return ctxerrors.Wrap(err, "seal the evaluation input")
	}

	record.EncryptedInput = sealed

	// The evaluation row and its idempotency key are written together. A
	// key that outlived its evaluation would replay into a missing row.
	err = s.repo.Transaction(func(tx *repositories.Query) error {
		if err := tx.Evaluation.WithContext(ctx).Create(record); err != nil {
			return ctxerrors.Wrap(err, "record the evaluation")
		}

		if input.IdempotencyKey == "" {
			return nil
		}

		now := s.now()

		idempotency := &models.IdempotencyRecord{
			KeyHash:              hashKey(input.IdempotencyKey),
			CanonicalRequestHash: requestHash,
			EvaluationID:         record.ID,
			ExpiresAt:            now.Add(s.config.IdempotencyRetention),
			CreatedAt:            now,
		}

		if err := tx.IdempotencyRecord.WithContext(ctx).
			Create(idempotency); err != nil {
			return ctxerrors.Wrap(err, "record the idempotency key")
		}

		return nil
	})
	if err != nil {
		return ctxerrors.Wrap(err, "persist the evaluation")
	}

	return nil
}

// sealInput encrypts the raw request when retention is enabled. With
// retention off it returns nil, and nothing about the caller's state or
// facts is written anywhere.
func (s *Service) sealInput(input CreateInput) ([]byte, error) {
	if !s.config.StoreInputs || !s.sealer.Enabled() {
		return nil, nil
	}

	payload := struct {
		State    any               `json:"state"`
		Facts    map[string]any    `json:"facts"`
		Metadata map[string]string `json:"metadata"`
	}{
		State:    input.State,
		Facts:    input.Facts,
		Metadata: input.Metadata,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, ctxerrors.Wrap(
			commerr.ErrMarshalFailed, "encode the input for retention",
		)
	}

	sealed, err := s.sealer.Seal(encoded)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "seal the retained input")
	}

	return sealed, nil
}

// findEvaluation loads one row, translating the driver's not-found into the
// project's sentinel.
func (s *Service) findEvaluation(
	ctx context.Context,
	id uuid.UUID,
) (*models.Evaluation, error) {
	repo := s.repo.Evaluation

	record, err := repo.WithContext(ctx).Where(repo.ID.Eq(id)).First()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ctxerrors.Wrapf(
				commerr.ErrNotFound, "no evaluation %s", id,
			)
		}

		return nil, ctxerrors.Wrap(err, "load the evaluation")
	}

	return record, nil
}
