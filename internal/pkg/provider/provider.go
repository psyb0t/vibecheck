// Package provider defines the decision-model boundary Vibecheck evaluates
// against.
//
// The types here are Jev-shaped but transport-free: they carry typed questions
// and typed answers, never wire DTOs, HTTP status codes, or API request
// bodies. Core depends on this interface; only a concrete adapter under a
// subpackage knows how to speak to a real provider.
package provider

import (
	"context"
	"time"

	"github.com/psyb0t/vibecheck/internal/pkg/common/decision"
)

// Question is one typed question to ask about the state.
//
// Which criteria field is populated depends on Type, mirroring the same split
// the compiled policy makes.
type Question struct {
	ID           string
	Type         decision.QuestionType
	Instructions string

	// ChoiceCriteria maps option key to its rubric description. Choice only.
	ChoiceCriteria map[string]string

	// ScoreCriteria is the ordered low-to-high level descriptions, where the
	// index is the level number. Score only.
	ScoreCriteria []string
}

// Request is one provider call. Every question is evaluated against the same
// state in a single round trip.
type Request struct {
	// Model is the provider's model name. Empty means the adapter's
	// configured default.
	Model string

	// State is the content to judge: a string, or a JSON-encodable value for
	// structured input.
	State any

	Questions []Question
}

// Usage is what the call cost. Vibecheck records it per evaluation so an
// operator can attribute spend without keeping the inputs.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Attempt records one physical request. A response carries the whole series,
// including the failed attempts, so the audit trail shows retries without
// anything having to log them as they happen.
type Attempt struct {
	Number int

	// StatusCode is the HTTP status, or zero when the attempt failed before
	// a response arrived.
	StatusCode int

	Duration time.Duration

	// Err is why this attempt failed, nil for the attempt that succeeded.
	Err error
}

// Response is a successful provider call.
type Response struct {
	// Model is the versioned model that actually answered, which can differ
	// from the alias that was requested.
	Model string

	Answers  decision.Answers
	Usage    Usage
	Attempts []Attempt
	Duration time.Duration
}

// AttemptCount reports how many physical requests the call took.
func (r Response) AttemptCount() int {
	return len(r.Attempts)
}

// Provider evaluates typed questions about a state.
//
// Implementations must honor context cancellation, must not retry a request
// the caller cancelled, and must return an error wrapping one of this
// package's sentinels so callers can map failures without string matching.
type Provider interface {
	Evaluate(ctx context.Context, request Request) (Response, error)
}
