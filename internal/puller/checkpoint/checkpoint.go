// Package checkpoint binds opaque native capture positions to their durable
// source identity and capture contract. Checkpoints carry no cache identity.
package checkpoint

import "fmt"

type Checkpoint string

type ErrorCode string

const (
	InvalidCheckpoint  ErrorCode = "INVALID_CHECKPOINT"
	IncompatibleState  ErrorCode = "INCOMPATIBLE_STATE"
	SourceMismatch     ErrorCode = "SOURCE_MISMATCH"
	ScopeMismatch      ErrorCode = "SCOPE_MISMATCH"
	HistoryUnavailable ErrorCode = "HISTORY_UNAVAILABLE"
	SourceUnavailable  ErrorCode = "SOURCE_UNAVAILABLE"
)

// Error preserves the underlying failure for classification. Message must not
// contain checkpoint or resume-token contents; Cause is never rendered.
type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func (e *Error) Unwrap() error { return e.Cause }

// Validate checks the complete encoding and supported capture contract. Source
// availability and retained history are verified when capture opens its cursor.
func Validate(cp Checkpoint) error {
	_, _, err := DecodeMongo(cp)
	return err
}
