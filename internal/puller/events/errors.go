package events

import "fmt"

type ErrorCode string

const (
	CodeInvalidCursor      ErrorCode = "INVALID_CURSOR"
	CodeUnknownSource      ErrorCode = "UNKNOWN_SOURCE"
	CodeGenerationMismatch ErrorCode = "GENERATION_MISMATCH"
	CodeHistoryExpired     ErrorCode = "HISTORY_EXPIRED"
	CodePositionAhead      ErrorCode = "POSITION_AHEAD"
	CodeStorageFailure     ErrorCode = "STORAGE_FAILURE"
	CodeSourceUnavailable  ErrorCode = "SOURCE_UNAVAILABLE"
	CodeContinuityLost     ErrorCode = "CONTINUITY_LOST"
	CodeUnsupportedFormat  ErrorCode = "UNSUPPORTED_FORMAT"
	CodeOverloaded         ErrorCode = "OVERLOADED"
)

// Error carries the recovery decision across local and remote subscriptions.
// Source positions are safe diagnostic metadata; raw cursors and MongoDB resume
// tokens must never be attached. Cause retains the original local failure chain.
type Error struct {
	Code             ErrorCode
	Backend          string
	SourceID         string
	Generation       string
	DiscardedThrough uint64
	CommittedThrough uint64
	Cause            error
}

func (e *Error) Error() string {
	message := string(e.Code)
	if e.Backend != "" {
		message += fmt.Sprintf(" backend=%q", e.Backend)
	}
	if e.SourceID != "" {
		message += fmt.Sprintf(" source=%q", e.SourceID)
	}
	if e.Generation != "" {
		message += fmt.Sprintf(" generation=%q", e.Generation)
	}
	if e.Cause != nil {
		message += ": " + e.Cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Cause }
