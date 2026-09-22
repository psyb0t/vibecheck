package typesafe

import (
	"context"
	"time"
)

// QuestionType identifies one System One decision primitive.
type QuestionType string

const (
	QuestionTypeChoice QuestionType = "choice"
	QuestionTypeNoul   QuestionType = "noul"
	QuestionTypeScore  QuestionType = "score"
)

// QuestionInput asks one named question about the request state.
type QuestionInput struct {
	Type         QuestionType
	Instructions any
	Criteria     any
}

// Request evaluates every named question against the same state.
type Request struct {
	State     any
	Model     string
	Questions map[string]QuestionInput
}

// Decision is one normalized System One result.
type Decision struct {
	Type QuestionType `json:"type"`

	Choice string  `json:"choice,omitempty"`
	Score  float64 `json:"score,omitempty"`
	Noul   float64 `json:"noul,omitempty"`

	Confidence    float64 `json:"confidence,omitempty"`
	HasConfidence bool    `json:"hasConfidence"`

	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]any     `json:"legend,omitempty"`
}

// Decisions maps caller-chosen question names to their results.
type Decisions map[string]Decision

// TokenUsage reports the tokens billed for one evaluation.
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
}

// Attempt records one physical HTTP request.
type Attempt struct {
	Number     int
	StatusCode int
	Duration   time.Duration
	Err        error
}

// Response is a successful System One evaluation.
type Response struct {
	Model    string
	Answers  Decisions
	Usage    TokenUsage
	Attempts []Attempt
	Duration time.Duration
}

// AttemptCount reports how many physical requests were made.
func (r Response) AttemptCount() int {
	return len(r.Attempts)
}

// Client is the mockable public System One client contract.
type Client interface {
	Evaluate(ctx context.Context, request Request) (Response, error)
	Models(ctx context.Context) ([]ModelMetadata, error)
}

// AttemptObserver receives one record per completed physical request.
type AttemptObserver func(attempt Attempt)
