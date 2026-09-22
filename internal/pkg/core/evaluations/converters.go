package evaluations

import (
	"encoding/json"
	"maps"
	"sort"
	"time"

	"github.com/psyb0t/ctxerrors"
	"github.com/psyb0t/ctxerrors/commerr"
	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
	"github.com/psyb0t/vibecheck/internal/pkg/db/models"
	"github.com/psyb0t/vibecheck/internal/pkg/http/api"
	"github.com/psyb0t/vibecheck/internal/pkg/policy"
)

// newRecord builds the audit row with everything known before the decision
// is made. The decision phase fills in the rest.
func (s *Service) newRecord(
	compiled *policy.Compiled,
	kind PolicyKind,
	input CreateInput,
	startedAt time.Time,
) *models.Evaluation {
	metadataJSON, _ := encodeMetadata(input.Metadata)

	return &models.Evaluation{
		ID:                 s.newID(),
		PolicySnapshotHash: compiled.Hash,
		PolicyName:         compiled.Ref.Name,
		PolicyVersion:      compiled.Ref.Version,
		PolicyKind:         kind.String(),
		RequestedModel:     compiled.Model,
		RequestID:          input.RequestID,
		MetadataJSON:       metadataJSON,
		CreatedAt:          startedAt,
	}
}

// evaluationModelToAPI converts a persisted row to its public shape.
func evaluationModelToAPI(record *models.Evaluation) api.Evaluation {
	answers := decodeAnswersJSON(record.AnswersJSON)

	projected := api.Evaluation{
		Id: record.ID,
		Policy: api.PolicyRef{
			Name:    record.PolicyName,
			Version: record.PolicyVersion,
		},
		PolicyHash:    record.PolicySnapshotHash,
		PolicyKind:    api.PolicyKind(record.PolicyKind),
		Status:        api.EvaluationStatus(record.Status),
		Outcome:       record.Outcome,
		ExecutionPath: api.ExecutionPath(record.ExecutionPath),
		Answers:       answersToAPI(answers),
		Usage: api.Usage{
			InputTokens:  record.InputTokens,
			OutputTokens: record.OutputTokens,
		},
		CreatedAt: record.CreatedAt,
	}

	if record.MatchedRuleID != "" {
		matched := record.MatchedRuleID
		projected.MatchedRuleId = &matched
	}

	if record.FailureCode != "" {
		failure := record.FailureCode
		projected.FailureCode = &failure
	}

	assignOptionalEvaluationFields(&projected, record)

	return projected
}

// assignOptionalEvaluationFields fills the pointer-valued fields the
// generated type uses for optional properties.
func assignOptionalEvaluationFields(
	projected *api.Evaluation,
	record *models.Evaluation,
) {
	if record.RequestedModel != "" {
		requested := record.RequestedModel
		projected.RequestedModel = &requested
	}

	if record.ResolvedModel != "" {
		resolved := record.ResolvedModel
		projected.ResolvedModel = &resolved
	}

	if record.RequestID != "" {
		requestID := record.RequestID
		projected.RequestId = &requestID
	}

	attempts := record.ProviderAttempts
	projected.ProviderAttempts = &attempts

	providerDuration := int(record.ProviderDurationMS)
	projected.ProviderDurationMs = &providerDuration

	totalDuration := int(record.TotalDurationMS)
	projected.TotalDurationMs = &totalDuration

	if metadata := decodeMetadataJSON(record.MetadataJSON); len(metadata) > 0 {
		projected.Metadata = &metadata
	}
}

func answersToAPI(answers decision.Answers) map[string]api.Answer {
	projected := make(map[string]api.Answer, len(answers))

	for id, answer := range answers {
		entry := api.Answer{Type: api.QuestionType(answer.Type)}

		switch answer.Type {
		case decision.QuestionTypeNoul:
			noul := answer.Noul
			entry.Noul = &noul
		case decision.QuestionTypeChoice:
			choice := answer.Choice
			entry.Choice = &choice
			entry.Probabilities = probabilitiesToAPI(answer.Probabilities)
		case decision.QuestionTypeScore:
			score := answer.Score
			entry.Score = &score
			entry.Probabilities = probabilitiesToAPI(answer.Probabilities)
			entry.Legend = legendToAPI(answer.Legend)
		}

		if answer.HasConfidence {
			confidence := answer.Confidence
			entry.Confidence = &confidence
		}

		projected[id] = entry
	}

	return projected
}

func probabilitiesToAPI(source map[string]float64) *map[string]float64 {
	if len(source) == 0 {
		return nil
	}

	projected := make(map[string]float64, len(source))
	maps.Copy(projected, source)

	return &projected
}

func legendToAPI(source map[string]any) *map[string]any {
	if len(source) == 0 {
		return nil
	}

	projected := make(map[string]any, len(source))
	maps.Copy(projected, source)

	return &projected
}

func feedbackModelToAPI(record *models.EvaluationFeedback) api.Feedback {
	projected := api.Feedback{
		Id:              record.ID,
		EvaluationId:    record.EvaluationID,
		ExpectedOutcome: record.ExpectedOutcome,
		CreatedAt:       record.CreatedAt,
	}

	if record.Note != "" {
		note := record.Note
		projected.Note = &note
	}

	return projected
}

func encodeAnswers(answers decision.Answers) (string, error) {
	encoded, err := json.Marshal(answers)
	if err != nil {
		return "", ctxerrors.Wrap(
			commerr.ErrMarshalFailed, "encode answers for the audit record",
		)
	}

	return string(encoded), nil
}

// decodeAnswersJSON is tolerant on purpose: a row whose answers cannot be
// decoded still has a usable outcome, status, and identity, and refusing to
// return it would hide the audit trail rather than protect anything.
func decodeAnswersJSON(encoded string) decision.Answers {
	if encoded == "" {
		return decision.Answers{}
	}

	answers := decision.Answers{}
	if err := json.Unmarshal([]byte(encoded), &answers); err != nil {
		return decision.Answers{}
	}

	return answers
}

func encodeMetadata(metadata map[string]string) (string, error) {
	if len(metadata) == 0 {
		return "", nil
	}

	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", ctxerrors.Wrap(
			commerr.ErrMarshalFailed, "encode caller metadata",
		)
	}

	return string(encoded), nil
}

func decodeMetadataJSON(encoded string) map[string]string {
	if encoded == "" {
		return nil
	}

	metadata := map[string]string{}
	if err := json.Unmarshal([]byte(encoded), &metadata); err != nil {
		return nil
	}

	return metadata
}

func encodeDocument(document map[string]any) ([]byte, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ctxerrors.Wrap(
			commerr.ErrMarshalFailed, "re-encode the inline policy document",
		)
	}

	return encoded, nil
}

// sortedKeys returns map keys in a stable order, so anything derived from a
// map (a hash, an error message) is reproducible.
func sortedKeys[V any](source map[string]V) []string {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	return keys
}
