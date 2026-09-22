package typesafe

import (
	"encoding/json"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
)

const (
	maxChoiceCriteria = 255
	minScoreCriteria  = 2
	maxScoreCriteria  = 10
)

func buildRequest(
	request Request,
	defaultModel string,
) (SystemoneV1SystemonePostJSONRequestBody, error) {
	if len(request.Questions) == 0 {
		return SystemOneRequest{}, ctxerrors.Wrap(
			commerr.ErrInvalidArgument,
			"a System One request needs at least one question",
		)
	}

	state := SystemOneRequest_State{}
	if err := marshalIntoUnion(request.State, &state); err != nil {
		return SystemOneRequest{}, ctxerrors.Wrap(
			err,
			"encode System One state",
		)
	}

	questions := make(map[string]Question, len(request.Questions))
	for name, question := range request.Questions {
		if name == "" {
			return SystemOneRequest{}, ctxerrors.Wrap(
				commerr.ErrInvalidArgument,
				"System One question name is empty",
			)
		}

		wireQuestion, err := buildQuestion(question)
		if err != nil {
			return SystemOneRequest{}, ctxerrors.Wrapf(
				err,
				"build System One question %q",
				name,
			)
		}

		questions[name] = wireQuestion
	}

	model := request.Model
	if model == "" {
		model = defaultModel
	}

	return SystemOneRequest{
		State:     state,
		Model:     model,
		Questions: questions,
	}, nil
}

func buildQuestion(question QuestionInput) (Question, error) {
	criteria, err := normalizedQuestionCriteria(question)
	if err != nil {
		return Question{}, err
	}

	raw := map[string]any{
		"type": question.Type,
	}
	if question.Instructions != nil {
		raw["instructions"] = question.Instructions
	}

	if criteria != nil {
		raw["criteria"] = criteria
	}

	wireQuestion := Question{}
	if err := marshalIntoUnion(raw, &wireQuestion); err != nil {
		return Question{}, ctxerrors.Wrap(
			err,
			"encode System One question",
		)
	}

	return wireQuestion, nil
}

func normalizedQuestionCriteria(question QuestionInput) (any, error) {
	switch question.Type {
	case QuestionTypeNoul:
		return question.Criteria, nil
	case QuestionTypeChoice:
		return normalizedChoiceCriteria(question.Criteria)
	case QuestionTypeScore:
		return normalizedScoreCriteria(question.Criteria)
	default:
		return nil, ctxerrors.Wrapf(
			commerr.ErrInvalidArgument,
			"unsupported System One question type %q",
			question.Type,
		)
	}
}

func normalizedChoiceCriteria(raw any) (map[string]any, error) {
	criteria, ok := raw.(map[string]any)
	if !ok || len(criteria) == 0 || len(criteria) > maxChoiceCriteria {
		return nil, ctxerrors.Wrap(
			commerr.ErrInvalidArgument,
			"choice criteria must contain between 1 and 255 options",
		)
	}

	return criteria, nil
}

func normalizedScoreCriteria(raw any) ([]any, error) {
	criteria, ok := raw.([]any)
	if !ok ||
		len(criteria) < minScoreCriteria ||
		len(criteria) > maxScoreCriteria {
		return nil, ctxerrors.Wrap(
			commerr.ErrInvalidArgument,
			"score criteria must contain between 2 and 10 levels",
		)
	}

	return criteria, nil
}

func normalizeResponse(response SystemOneResponse) (Response, error) {
	if response.Model == "" {
		return Response{}, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"TypeSafe response names no model",
		)
	}

	answers := make(Decisions, len(response.Answers))
	for name, wireAnswer := range response.Answers {
		answer, err := normalizeAnswer(wireAnswer)
		if err != nil {
			return Response{}, ctxerrors.Wrapf(
				err,
				"decode TypeSafe answer %q",
				name,
			)
		}

		answers[name] = answer
	}

	return Response{
		Model:   response.Model,
		Answers: answers,
		Usage: TokenUsage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
		},
	}, nil
}

func normalizeAnswer(wireAnswer Answer) (Decision, error) {
	discriminator, err := wireAnswer.Discriminator()
	if err != nil {
		return Decision{}, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"read TypeSafe answer discriminator",
		)
	}

	switch QuestionType(discriminator) {
	case QuestionTypeNoul:
		return normalizeNoulAnswer(wireAnswer)
	case QuestionTypeChoice:
		return normalizeChoiceAnswer(wireAnswer)
	case QuestionTypeScore:
		return normalizeScoreAnswer(wireAnswer)
	default:
		return Decision{}, ctxerrors.Wrapf(
			commerr.ErrParseFailed,
			"unsupported TypeSafe answer type %q",
			discriminator,
		)
	}
}

func normalizeNoulAnswer(wireAnswer Answer) (Decision, error) {
	wire, err := wireAnswer.AsNoulAnswer()
	if err != nil {
		return Decision{}, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"decode Noul answer",
		)
	}

	return Decision{Type: QuestionTypeNoul, Noul: float64(wire.Noul)}, nil
}

func normalizeChoiceAnswer(wireAnswer Answer) (Decision, error) {
	wire, err := wireAnswer.AsChoiceAnswer()
	if err != nil {
		return Decision{}, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"decode Choice answer",
		)
	}

	return Decision{
		Type:          QuestionTypeChoice,
		Choice:        wire.Choice,
		Confidence:    float64(wire.Confidence),
		HasConfidence: true,
		Probabilities: floatMap(wire.Probabilities),
	}, nil
}

func normalizeScoreAnswer(wireAnswer Answer) (Decision, error) {
	wire, err := wireAnswer.AsScoreAnswer()
	if err != nil {
		return Decision{}, ctxerrors.Wrap(
			commerr.ErrParseFailed,
			"decode Score answer",
		)
	}

	legend := make(map[string]any, len(wire.Legend))
	for level, value := range wire.Legend {
		decoded, err := decodeUnion(value)
		if err != nil {
			return Decision{}, ctxerrors.Wrapf(
				err,
				"decode Score legend level %q",
				level,
			)
		}

		legend[level] = decoded
	}

	return Decision{
		Type:          QuestionTypeScore,
		Score:         float64(wire.Score),
		Confidence:    float64(wire.Confidence),
		HasConfidence: true,
		Probabilities: floatMap(wire.Probabilities),
		Legend:        legend,
	}, nil
}

func marshalIntoUnion(value any, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ctxerrors.Wrap(err, "marshal union value")
	}

	if err := json.Unmarshal(encoded, target); err != nil {
		return ctxerrors.Wrap(err, "unmarshal union value")
	}

	return nil
}

func decodeUnion(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "marshal generated union value")
	}

	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, ctxerrors.Wrap(err, "unmarshal generated union value")
	}

	return decoded, nil
}

func floatMap(values map[string]float32) map[string]float64 {
	converted := make(map[string]float64, len(values))
	for key, value := range values {
		converted[key] = float64(value)
	}

	return converted
}
