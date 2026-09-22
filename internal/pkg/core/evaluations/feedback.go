package evaluations

import (
	"context"
	"slices"

	"github.com/google/uuid"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/db/models"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// MaxFeedbackNoteLength bounds the free-text note. It matches the ceiling the
// API contract advertises.
const MaxFeedbackNoteLength = 2000

// FeedbackInput is one operator label for a past decision.
type FeedbackInput struct {
	EvaluationID    uuid.UUID
	ExpectedOutcome string
	Note            string
}

// SubmitFeedback records the outcome a human expected.
//
// It never mutates the evaluation. Feedback is calibration data: it exists so
// an operator can later measure how often the policy agreed with them, and
// letting it edit a recorded decision would destroy exactly that evidence.
func (s *Service) SubmitFeedback(
	ctx context.Context,
	input FeedbackInput,
) (api.Feedback, error) {
	if err := validateFeedback(input); err != nil {
		return api.Feedback{}, err
	}

	evaluation, err := s.findEvaluation(ctx, input.EvaluationID)
	if err != nil {
		return api.Feedback{}, err
	}

	if err := s.validateExpectedOutcome(evaluation, input); err != nil {
		return api.Feedback{}, err
	}

	record := &models.EvaluationFeedback{
		ID:              s.newID(),
		EvaluationID:    evaluation.ID,
		ExpectedOutcome: input.ExpectedOutcome,
		Note:            input.Note,
		CreatedAt:       s.now(),
	}

	if err := s.repo.EvaluationFeedback.WithContext(ctx).
		Create(record); err != nil {
		return api.Feedback{}, ctxerrors.Wrap(err, "record the feedback")
	}

	return feedbackModelToAPI(record), nil
}

// validateExpectedOutcome refuses an outcome the judged policy never
// declared.
//
// Feedback exists to measure how often a policy agreed with a human. An
// expected outcome outside that policy's own set cannot be compared with
// anything, so recording it would only add noise to the calibration data.
//
// An inline policy is not in the loaded set, so its outcomes cannot be
// resolved after the fact. Those keep the shape check alone.
func (s *Service) validateExpectedOutcome(
	evaluation *models.Evaluation,
	input FeedbackInput,
) error {
	compiled, err := s.policies.Compiled(policy.Ref{
		Name:    evaluation.PolicyName,
		Version: evaluation.PolicyVersion,
	})
	if err != nil {
		return nil //nolint:nilerr // an inline policy has no loaded set
	}

	expected := decision.Outcome(input.ExpectedOutcome)
	if slices.Contains(compiled.Outcomes, expected) {
		return nil
	}

	return ctxerrors.Wrapf(
		commerr.ErrValidationFailed,
		"policy %s %s does not declare outcome %q",
		evaluation.PolicyName, evaluation.PolicyVersion,
		input.ExpectedOutcome,
	)
}

func validateFeedback(input FeedbackInput) error {
	outcome := decision.Outcome(input.ExpectedOutcome)
	if !outcome.IsValid() {
		return ctxerrors.Wrapf(
			commerr.ErrValidationFailed,
			"expected outcome %q is not a valid outcome name",
			input.ExpectedOutcome,
		)
	}

	if len(input.Note) > MaxFeedbackNoteLength {
		return ctxerrors.Wrapf(
			commerr.ErrValidationFailed,
			"note exceeds %d characters", MaxFeedbackNoteLength,
		)
	}

	return nil
}
