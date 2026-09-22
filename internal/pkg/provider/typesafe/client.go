package typesafe

import (
	"context"
	"errors"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
	typesafeapi "github.com/psyb0t/vibecheck/pkg/gotypesafe"
)

// Client adapts the public TypeSafe client to Vibecheck's decision boundary.
type Client struct {
	client typesafeapi.Client
}

// AttemptObserver receives one record per physical request.
type AttemptObserver func(attempt provider.Attempt)

type options struct {
	clientOptions []typesafeapi.Option
}

// Option adjusts the adapter and its public client.
type Option func(*options)

// WithAttemptObserver installs a per-attempt observer.
func WithAttemptObserver(observe AttemptObserver) Option {
	return func(options *options) {
		options.clientOptions = append(
			options.clientOptions,
			typesafeapi.WithAttemptObserver(func(attempt typesafeapi.Attempt) {
				observe(provider.Attempt{
					Number:     attempt.Number,
					StatusCode: attempt.StatusCode,
					Duration:   attempt.Duration,
					Err:        attempt.Err,
				})
			}),
		)
	}
}

// New builds the Vibecheck adapter around the public client.
func New(config Config, adapterOptions ...Option) (*Client, error) {
	resolved := options{}
	for _, option := range adapterOptions {
		option(&resolved)
	}

	client, err := typesafeapi.New(config, resolved.clientOptions...)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "build public TypeSafe client")
	}

	return newWithClient(client), nil
}

func newWithClient(client typesafeapi.Client) *Client {
	return &Client{client: client}
}

// Evaluate converts Vibecheck's compiled policy into one System One call.
func (c *Client) Evaluate(
	ctx context.Context,
	request provider.Request,
) (provider.Response, error) {
	questions := make(
		map[string]typesafeapi.QuestionInput,
		len(request.Questions),
	)
	for _, question := range request.Questions {
		if _, exists := questions[question.ID]; exists {
			return provider.Response{}, ctxerrors.Wrapf(
				commerr.ErrInvalidArgument,
				"question %q is asked twice",
				question.ID,
			)
		}

		questions[question.ID] = typesafeapi.QuestionInput{
			Type:         typesafeapi.QuestionType(question.Type),
			Instructions: question.Instructions,
			Criteria:     questionCriteria(question),
		}
	}

	response, err := c.client.Evaluate(ctx, typesafeapi.Request{
		State:     request.State,
		Model:     request.Model,
		Questions: questions,
	})
	if err != nil {
		return provider.Response{}, mapError(err)
	}

	return provider.Response{
		Model:    response.Model,
		Answers:  answers(response.Answers),
		Usage:    provider.Usage(response.Usage),
		Attempts: attempts(response.Attempts),
		Duration: response.Duration,
	}, nil
}

func questionCriteria(question provider.Question) any {
	switch question.Type {
	case decision.QuestionTypeNoul:
		return question.NoulCriteria
	case decision.QuestionTypeChoice:
		return question.ChoiceCriteria
	case decision.QuestionTypeScore:
		return question.ScoreCriteria
	}

	return nil
}

func answers(source typesafeapi.Decisions) decision.Answers {
	converted := make(decision.Answers, len(source))
	for id, answer := range source {
		converted[id] = decision.Answer{
			Type:          decision.QuestionType(answer.Type),
			Choice:        answer.Choice,
			Score:         answer.Score,
			Noul:          answer.Noul,
			Confidence:    answer.Confidence,
			HasConfidence: answer.HasConfidence,
			Probabilities: answer.Probabilities,
			Legend:        answer.Legend,
		}
	}

	return converted
}

func attempts(source []typesafeapi.Attempt) []provider.Attempt {
	converted := make([]provider.Attempt, len(source))
	for index, attempt := range source {
		converted[index] = provider.Attempt{
			Number:     attempt.Number,
			StatusCode: attempt.StatusCode,
			Duration:   attempt.Duration,
			Err:        attempt.Err,
		}
	}

	return converted
}

func mapError(err error) error {
	switch {
	case errors.Is(err, typesafeapi.ErrResponseTooLarge):
		return ctxerrors.Wrap(
			provider.ErrResponseTooLarge,
			"TypeSafe response refused",
		)
	case errors.Is(err, typesafeapi.ErrOverloaded):
		return ctxerrors.Wrap(provider.ErrOverloaded, "TypeSafe is overloaded")
	default:
		return ctxerrors.Wrap(err, "evaluate with public TypeSafe client")
	}
}
