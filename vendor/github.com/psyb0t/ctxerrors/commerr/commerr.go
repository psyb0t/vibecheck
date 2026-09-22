// Package commerr provides common, general-purpose sentinel errors — not-found,
// already-exists, invalid-argument, timeout, unavailable, and the like — so any
// Go code can return and match a shared one with errors.Is instead of
// re-declaring the same errors in every package.
//
// Each is a plain errors.New value, so it survives any wrapping (fmt.Errorf's
// %w, ctxerrors.Wrap, ...) — errors.Is(err, commerr.ErrX) stays true through
// every layer, and any of them works as a target for ctxerrors.SetErrorMap
// when translating a foreign driver error into a business sentinel.
package commerr

import "errors"

var (
	// Configuration & Environment errors
	ErrEnvVarNotSet              = errors.New("env var is not set")
	ErrRequiredConfigValueNotSet = errors.New("required config value is not set")
	ErrEmptyMigrationsPath       = errors.New("migrations path is empty")

	// File & Path errors
	ErrFileInvalid           = errors.New("invalid file")
	ErrFileNotFound          = errors.New("file not found")
	ErrPathIsRequired        = errors.New("path is required")
	ErrCouldNotDownloadFiles = errors.New("could not download files")

	// Validation & Input errors
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrInvalidValue     = errors.New("invalid value")
	ErrTargetNotPointer = errors.New("target is not a pointer")
	ErrCouldNotDecode   = errors.New("could not decode")
	ErrValidationFailed = errors.New("validation failed")
	ErrOutOfRange       = errors.New("out of range")

	// Field & Data errors
	ErrNilOutput                      = errors.New("output is nil")
	ErrNilField                       = errors.New("field is nil")
	ErrRequiredFieldNotSet            = errors.New("required field is not set")
	ErrRequiredLLMResponseFieldNotSet = errors.New("required llm response field is not set")
	ErrAlreadyExists                  = errors.New("already exists")

	// Job & Process errors
	ErrJobFailed                 = errors.New("job failed")
	ErrUnexpectedNumberOfResults = errors.New("unexpected number of results")
	ErrNotFound                  = errors.New("not found")

	// Operation errors
	ErrFetchFailed     = errors.New("fetch failed")
	ErrParseFailed     = errors.New("parse failed")
	ErrReadFailed      = errors.New("read failed")
	ErrWriteFailed     = errors.New("write failed")
	ErrOpenFailed      = errors.New("open failed")
	ErrCloseFailed     = errors.New("close failed")
	ErrExecFailed      = errors.New("exec failed")
	ErrPublishFailed   = errors.New("publish failed")
	ErrSubscribeFailed = errors.New("subscribe failed")
	ErrDownloadFailed  = errors.New("download failed")
	ErrUploadFailed    = errors.New("upload failed")
	ErrUpsertFailed    = errors.New("upsert failed")
	ErrDeleteFailed    = errors.New("delete failed")
	ErrConnectFailed   = errors.New("connect failed")
	ErrBrowseFailed    = errors.New("browse failed")
	ErrSeedFailed      = errors.New("seed failed")
	ErrMigrationFailed = errors.New("migration failed")
	ErrUnmarshalFailed = errors.New("unmarshal failed")
	ErrMarshalFailed   = errors.New("marshal failed")

	// Capability errors
	ErrNotImplemented = errors.New("not implemented")
	ErrUnsupported    = errors.New("unsupported")

	// Process State errors
	//
	// ErrUnavailable is for a capability the process knows about but that was
	// never wired: an optional dependency left unconfigured, a feature switched
	// off, a subsystem that failed to start. Deliberately distinct from
	// ErrNotFound — a lookup that came back empty tells the caller to fix the
	// query, an unavailable capability tells them to fix the configuration.
	ErrUnavailable  = errors.New("unavailable")
	ErrFailed       = errors.New("failed")
	ErrTimeout      = errors.New("timeout")
	ErrTerminated   = errors.New("terminated")
	ErrKilled       = errors.New("killed")
	ErrClosing      = errors.New("closing")
	ErrClosed       = errors.New("closed")
	ErrShuttingDown = errors.New("shutting down")
	ErrCancelled    = errors.New("cancelled")
	ErrNotReady     = errors.New("not ready")
	ErrInvalidState = errors.New("invalid state")
	ErrExpired      = errors.New("expired")

	// Concurrency & Capacity errors
	ErrLockHeld  = errors.New("lock held")
	ErrConflict  = errors.New("conflict")
	ErrExhausted = errors.New("exhausted")

	// Access & rate-limit errors
	ErrNotAuthenticated = errors.New("not authenticated")
	ErrPermissionDenied = errors.New("permission denied")
	ErrRateLimited      = errors.New("rate limited")
)
