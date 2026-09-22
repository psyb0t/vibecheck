package vibecheckserver

import (
	"context"
	"database/sql/driver"
	"time"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxscope"
	"github.com/psyb0t/vibecheck/internal/pkg/db/models"
	"github.com/psyb0t/vibecheck/internal/pkg/db/repositories"
	"github.com/psyb0t/vibecheck/internal/pkg/metrics"
)

// retentionResult counts what one cleanup pass removed, keyed by the bounded
// operation label the metric uses.
type retentionResult map[string]int64

// runRetention runs cleanup on an interval until the context is cancelled.
//
// A failed pass is logged and the loop continues. Retention is housekeeping:
// a transient database error must not take the service down, and the next
// tick retries the same work.
func runRetention(
	ctx context.Context,
	repo *repositories.Query,
	recorder *metrics.Metrics,
	settings retentionSettings,
) {
	logger := ctxscope.GetLogger(ctx)

	ticker := time.NewTicker(settings.every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("retention loop stopped")

			return
		case <-ticker.C:
			deleted, err := cleanup(ctx, repo, settings)
			recorder.ObserveRetentionRun(err != nil, deleted)

			if err != nil {
				logger.Error("retention pass failed", "err", err)

				continue
			}

			logger.Info(
				"retention pass complete",
				"evaluations", deleted[retentionOpEvaluations],
				"feedback", deleted[retentionOpFeedback],
				"idempotency_records", deleted[retentionOpIdempotency],
			)
		}
	}
}

// retentionSettings is the cleanup schedule and the evaluation window.
//
// There is no idempotency window here. Replay keys carry their own expiry
// column, written when the key was first used, so they age off that value
// rather than off whatever the current setting happens to be.
type retentionSettings struct {
	every time.Duration

	// evaluations is how long a recorded decision is kept. Zero keeps
	// evaluations forever.
	evaluations time.Duration
}

// cleanup removes expired rows and reports what went.
func cleanup(
	ctx context.Context,
	repo *repositories.Query,
	settings retentionSettings,
) (retentionResult, error) {
	now := time.Now()
	deleted := retentionResult{}

	idempotencyRemoved, err := expireIdempotency(ctx, repo, now)
	deleted[retentionOpIdempotency] = idempotencyRemoved

	if err != nil {
		return deleted, err
	}

	if settings.evaluations <= 0 {
		return deleted, nil
	}

	cutoff := now.Add(-settings.evaluations)

	evaluationsRemoved, feedbackRemoved, err := expireEvaluations(
		ctx, repo, cutoff,
	)
	deleted[retentionOpEvaluations] = evaluationsRemoved
	deleted[retentionOpFeedback] = feedbackRemoved

	return deleted, err
}

// expireIdempotency drops replay keys whose window has passed.
//
// These carry their own expiry column rather than being aged off created_at,
// because the retention window is chosen per deployment and a key written
// under an older setting must still expire when it said it would.
func expireIdempotency(
	ctx context.Context,
	repo *repositories.Query,
	now time.Time,
) (int64, error) {
	record := repo.IdempotencyRecord

	result, err := record.WithContext(ctx).
		Where(record.ExpiresAt.Lt(now)).
		Limit(retentionBatchSize).
		Delete()
	if err != nil {
		return 0, ctxerrors.Wrap(err, "delete expired idempotency records")
	}

	return result.RowsAffected, nil
}

// expireEvaluations drops aged-out evaluations and the feedback attached to
// them.
//
// Feedback is deleted by evaluation ID rather than by its own age. A human
// can label a decision long after it was made, and ageing feedback off its
// own created_at would leave those rows pointing at evaluations that are
// already gone.
func expireEvaluations(
	ctx context.Context,
	repo *repositories.Query,
	cutoff time.Time,
) (int64, int64, error) {
	evaluation := repo.Evaluation

	expired, err := evaluation.WithContext(ctx).
		Where(evaluation.CreatedAt.Lt(cutoff)).
		Limit(retentionBatchSize).
		Find()
	if err != nil {
		return 0, 0, ctxerrors.Wrap(err, "find expired evaluations")
	}

	if len(expired) == 0 {
		return 0, 0, nil
	}

	ids := evaluationIDs(expired)

	feedback := repo.EvaluationFeedback

	feedbackResult, err := feedback.WithContext(ctx).
		Where(feedback.EvaluationID.In(ids...)).
		Delete()
	if err != nil {
		return 0, 0, ctxerrors.Wrap(
			err,
			"delete feedback for expired evaluations",
		)
	}

	evaluationResult, err := evaluation.WithContext(ctx).
		Where(evaluation.ID.In(ids...)).
		Delete()
	if err != nil {
		return 0, feedbackResult.RowsAffected, ctxerrors.Wrap(
			err, "delete expired evaluations",
		)
	}

	return evaluationResult.RowsAffected, feedbackResult.RowsAffected, nil
}

// evaluationIDs collects the IDs for an IN clause.
//
// The generated field helpers take driver.Valuer rather than the column's Go
// type, so the slice is built at that interface instead of converted after
// the fact.
func evaluationIDs(records []*models.Evaluation) []driver.Valuer {
	ids := make([]driver.Valuer, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}

	return ids
}
