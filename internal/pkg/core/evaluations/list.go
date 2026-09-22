package evaluations

import (
	"context"

	"github.com/google/uuid"
	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/db/repositories"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
)

// ListFilter is the bounded filter set the list endpoint accepts.
type ListFilter struct {
	Limit  int
	Offset int

	PolicyName    string
	PolicyVersion string
	Outcome       string
	Status        string
}

// Get returns one recorded evaluation.
func (s *Service) Get(
	ctx context.Context,
	id uuid.UUID,
) (api.Evaluation, error) {
	record, err := s.findEvaluation(ctx, id)
	if err != nil {
		return api.Evaluation{}, err
	}

	return evaluationModelToAPI(record), nil
}

// List returns a page of evaluations, newest first with the ID as a stable
// tiebreaker.
//
// The envelope reports hasMore rather than a total. The evaluation table
// grows without bound, so counting it on every list call would get slower
// forever; a single indexed probe one page past the end does not.
func (s *Service) List(
	ctx context.Context,
	filter ListFilter,
) (api.EvaluationList, error) {
	if filter.Limit <= 0 || filter.Offset < 0 {
		return api.EvaluationList{}, ctxerrors.Wrap(
			commerr.ErrInvalidArgument,
			"limit must be positive and offset cannot be negative",
		)
	}

	repo := s.repo.Evaluation

	rows, err := s.filtered(ctx, filter).
		Order(repo.CreatedAt.Desc(), repo.ID.Desc()).
		Limit(filter.Limit).
		Offset(filter.Offset).
		Find()
	if err != nil {
		return api.EvaluationList{}, ctxerrors.Wrap(err, "list evaluations")
	}

	probe, err := s.filtered(ctx, filter).
		Order(repo.CreatedAt.Desc(), repo.ID.Desc()).
		Limit(1).
		Offset(filter.Offset + filter.Limit).
		Find()
	if err != nil {
		return api.EvaluationList{}, ctxerrors.Wrap(
			err, "probe for a further page of evaluations",
		)
	}

	items := make([]api.Evaluation, 0, len(rows))
	for _, row := range rows {
		items = append(items, evaluationModelToAPI(row))
	}

	return api.EvaluationList{
		Items:   items,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
		HasMore: len(probe) > 0,
	}, nil
}

// filtered applies the bounded filter set. Every comparison goes through a
// typed field accessor, so renaming a column breaks the build rather than
// silently matching nothing.
func (s *Service) filtered(
	ctx context.Context,
	filter ListFilter,
) repositories.IEvaluationDo {
	repo := s.repo.Evaluation
	query := repo.WithContext(ctx)

	if filter.PolicyName != "" {
		query = query.Where(repo.PolicyName.Eq(filter.PolicyName))
	}

	if filter.PolicyVersion != "" {
		query = query.Where(repo.PolicyVersion.Eq(filter.PolicyVersion))
	}

	if filter.Outcome != "" {
		query = query.Where(repo.Outcome.Eq(filter.Outcome))
	}

	if filter.Status != "" {
		query = query.Where(repo.Status.Eq(filter.Status))
	}

	return query
}
