// Package models holds the GORM model structs for Vibecheck's tables.
//
// The structs are the input to gorm-gen, which generates the typed
// repositories in internal/pkg/db/repositories. They carry no schema: column
// types, indexes, and constraints live in the SQL migrations, which are the
// single source of truth for the database shape. AutoMigrate is never used.
package models

import (
	"time"

	"github.com/google/uuid"
)

// Table names. They are referenced by the migrations and by the models, so
// they get names rather than being repeated as literals.
const (
	TablePolicySnapshots    = "policy_snapshots"
	TableEvaluations        = "evaluations"
	TableIdempotencyRecords = "idempotency_records"
	TableEvaluationFeedback = "evaluation_feedback"
)

// EvaluationStatus is how an evaluation ended.
type EvaluationStatus string

const (
	// EvaluationStatusCompleted means an outcome was selected, whether by a
	// pre-rule, a decision rule, or the default.
	EvaluationStatusCompleted EvaluationStatus = "completed"
	// EvaluationStatusProviderFailed means the provider could not be
	// consulted and no outcome was selected.
	EvaluationStatusProviderFailed EvaluationStatus = "provider_failed"
	// EvaluationStatusRejected means the request never reached the
	// provider because it failed the policy's input contract.
	EvaluationStatusRejected EvaluationStatus = "rejected"
)

func (s EvaluationStatus) String() string {
	return string(s)
}

func (s EvaluationStatus) IsValid() bool {
	switch s {
	case EvaluationStatusCompleted,
		EvaluationStatusProviderFailed,
		EvaluationStatusRejected:
		return true
	}

	return false
}

// PolicySnapshot is the exact policy an evaluation was decided by.
//
// The hash is the primary key, so re-loading an unchanged policy after a
// restart reuses the same row and every historical evaluation keeps pointing
// at the bytes that actually decided it.
type PolicySnapshot struct {
	Hash          string    `gorm:"column:hash;primaryKey"`
	Name          string    `gorm:"column:name"`
	Version       string    `gorm:"column:version"`
	CanonicalJSON string    `gorm:"column:canonical_json"`
	LoadedAt      time.Time `gorm:"column:loaded_at"`
}

func (PolicySnapshot) TableName() string {
	return TablePolicySnapshots
}

// Evaluation is the audit record for one decision.
//
// Raw state and facts are absent unless encrypted retention is explicitly
// enabled, in which case EncryptedInput holds the AEAD ciphertext and nothing
// else in the row reveals the input.
type Evaluation struct {
	ID uuid.UUID `gorm:"column:id;primaryKey"`

	PolicySnapshotHash string `gorm:"column:policy_snapshot_hash"`
	PolicyName         string `gorm:"column:policy_name"`
	PolicyVersion      string `gorm:"column:policy_version"`

	// PolicyKind is "named" or "inline". It is the bounded label metrics
	// use, so the exact identity never becomes a metric dimension.
	PolicyKind string `gorm:"column:policy_kind"`

	Status        string `gorm:"column:status"`
	Outcome       string `gorm:"column:outcome"`
	MatchedRuleID string `gorm:"column:matched_rule_id"`
	ExecutionPath string `gorm:"column:execution_path"`

	RequestedModel string `gorm:"column:requested_model"`
	ResolvedModel  string `gorm:"column:resolved_model"`

	// AnswersJSON is the canonical typed answer set. It holds model output,
	// never caller input.
	AnswersJSON string `gorm:"column:answers_json"`

	InputTokens  int `gorm:"column:input_tokens"`
	OutputTokens int `gorm:"column:output_tokens"`

	ProviderAttempts   int   `gorm:"column:provider_attempts"`
	ProviderDurationMS int64 `gorm:"column:provider_duration_ms"`
	TotalDurationMS    int64 `gorm:"column:total_duration_ms"`

	RequestID string `gorm:"column:request_id"`

	// MetadataJSON is caller metadata that already passed the key allowlist
	// and size checks.
	MetadataJSON string `gorm:"column:metadata_json"`

	// EncryptedInput is nil unless retention is enabled.
	EncryptedInput []byte `gorm:"column:encrypted_input"`

	// FailureCode is the stable error class when Status is not completed.
	FailureCode string `gorm:"column:failure_code"`

	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Evaluation) TableName() string {
	return TableEvaluations
}

// IdempotencyRecord ties a caller's idempotency key to the evaluation it
// produced.
//
// Only hashes are stored: the key itself is caller-chosen and the request may
// contain state, so neither is kept in the clear.
type IdempotencyRecord struct {
	KeyHash              string    `gorm:"column:key_hash;primaryKey"`
	CanonicalRequestHash string    `gorm:"column:canonical_request_hash"`
	EvaluationID         uuid.UUID `gorm:"column:evaluation_id"`
	ExpiresAt            time.Time `gorm:"column:expires_at"`
	CreatedAt            time.Time `gorm:"column:created_at"`
}

func (IdempotencyRecord) TableName() string {
	return TableIdempotencyRecords
}

// EvaluationFeedback is an operator's label for a past decision. It is
// calibration data, never an input to a future decision.
type EvaluationFeedback struct {
	ID              uuid.UUID `gorm:"column:id;primaryKey"`
	EvaluationID    uuid.UUID `gorm:"column:evaluation_id"`
	ExpectedOutcome string    `gorm:"column:expected_outcome"`
	Note            string    `gorm:"column:note"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (EvaluationFeedback) TableName() string {
	return TableEvaluationFeedback
}
