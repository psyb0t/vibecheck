package jev

import (
	"maps"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/provider"
)

// The wire types below mirror the documented System One request and response
// exactly. They are unexported because nothing outside this package should
// ever hold a provider DTO.

type systemOneRequest struct {
	State     any                     `json:"state"`
	Model     string                  `json:"model"`
	Questions map[string]wireQuestion `json:"questions"`
}

// wireQuestion carries the criteria shape the question type requires.
// Criteria is `any` because a choice sends an object and a score sends an
// array; a noul omits it.
type wireQuestion struct {
	Type         decision.QuestionType `json:"type"`
	Instructions string                `json:"instructions"`
	Criteria     any                   `json:"criteria,omitempty"`
}

type systemOneResponse struct {
	Model   string                `json:"model"`
	Answers map[string]wireAnswer `json:"answers"`
	Usage   wireUsage             `json:"usage"`
}

// wireAnswer is the union of the three answer shapes. Pointers distinguish an
// absent field from a zero value, which matters because a noul of 0.0 and a
// missing noul mean completely different things.
type wireAnswer struct {
	Type          decision.QuestionType `json:"type"`
	Choice        string                `json:"choice,omitempty"`
	Score         *float64              `json:"score,omitempty"`
	Noul          *float64              `json:"noul,omitempty"`
	Confidence    *float64              `json:"confidence,omitempty"`
	Probabilities map[string]float64    `json:"probabilities,omitempty"`
	Legend        map[string]string     `json:"legend,omitempty"`
}

type wireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// buildRequest converts a provider request into the wire shape.
func buildRequest(
	request provider.Request,
	defaultModel string,
) (systemOneRequest, error) {
	if len(request.Questions) == 0 {
		return systemOneRequest{}, ctxerrors.Wrap(
			commerr.ErrInvalidArgument,
			"a provider call needs at least one question",
		)
	}

	model := request.Model
	if model == "" {
		model = defaultModel
	}

	questions := make(map[string]wireQuestion, len(request.Questions))

	for _, question := range request.Questions {
		if _, duplicate := questions[question.ID]; duplicate {
			return systemOneRequest{}, ctxerrors.Wrapf(
				commerr.ErrInvalidArgument,
				"question %q is asked twice in one request", question.ID,
			)
		}

		entry, err := buildQuestion(question)
		if err != nil {
			return systemOneRequest{}, err
		}

		questions[question.ID] = entry
	}

	return systemOneRequest{
		State:     request.State,
		Model:     model,
		Questions: questions,
	}, nil
}

func buildQuestion(question provider.Question) (wireQuestion, error) {
	entry := wireQuestion{
		Type:         question.Type,
		Instructions: question.Instructions,
	}

	switch question.Type {
	case decision.QuestionTypeNoul:
		return entry, nil

	case decision.QuestionTypeChoice:
		// The criteria map is rebuilt rather than passed through so the
		// wire value cannot alias, and later mutate with, the compiled
		// policy's own map.
		criteria := make(map[string]string, len(question.ChoiceCriteria))
		maps.Copy(criteria, question.ChoiceCriteria)

		entry.Criteria = criteria

		return entry, nil

	case decision.QuestionTypeScore:
		entry.Criteria = append([]string(nil), question.ScoreCriteria...)

		return entry, nil
	}

	return wireQuestion{}, ctxerrors.Wrapf(
		commerr.ErrInvalidArgument,
		"question %q has unsupported type %q", question.ID, question.Type,
	)
}

// decodeAnswers converts the wire response into typed answers.
//
// It checks only what it takes to build a well-formed decision.Answer. Whether
// the answer set actually satisfies the policy that asked the questions is
// checked by the policy itself, which is the only thing that knows the
// declared questions.
func decodeAnswers(
	response systemOneResponse,
) (decision.Answers, error) {
	answers := make(decision.Answers, len(response.Answers))

	for id, wire := range response.Answers {
		answer, err := decodeAnswer(id, wire)
		if err != nil {
			return nil, err
		}

		answers[id] = answer
	}

	return answers, nil
}

func decodeAnswer(id string, wire wireAnswer) (decision.Answer, error) {
	answer := decision.Answer{Type: wire.Type}

	switch wire.Type {
	case decision.QuestionTypeNoul:
		if wire.Noul == nil {
			return decision.Answer{}, missingField(id, "noul")
		}

		answer.Noul = *wire.Noul

		return answer, nil

	case decision.QuestionTypeChoice:
		if wire.Choice == "" {
			return decision.Answer{}, missingField(id, "choice")
		}

		answer.Choice = wire.Choice
		answer.Probabilities = wire.Probabilities
		applyConfidence(&answer, wire.Confidence)

		return answer, nil

	case decision.QuestionTypeScore:
		if wire.Score == nil {
			return decision.Answer{}, missingField(id, "score")
		}

		answer.Score = *wire.Score
		answer.Probabilities = wire.Probabilities
		answer.Legend = wire.Legend
		applyConfidence(&answer, wire.Confidence)

		return answer, nil
	}

	return decision.Answer{}, ctxerrors.Wrapf(
		commerr.ErrParseFailed,
		"answer %q has unsupported type %q", id, wire.Type,
	)
}

func applyConfidence(answer *decision.Answer, confidence *float64) {
	if confidence == nil {
		return
	}

	answer.Confidence = *confidence
	answer.HasConfidence = true
}

func missingField(id, field string) error {
	return ctxerrors.Wrapf(
		commerr.ErrParseFailed, "answer %q has no %s field", id, field,
	)
}
