package types

import (
	"context"
	"fmt"
)

// WatchCheckpoint is a Store-issued, portable encoded continuation. Callers may
// persist and transmit it, but must not interpret it or compare positions by its
// byte order. Its source identity is independent of client and Puller instances.
type WatchCheckpoint string

type WatchOptions struct {
	// IncludeBefore requests available before-state. It does not require retained
	// historical images or change the meaning of the current Document enrichment.
	IncludeBefore bool
}

// WatchFrame advances a completed source prefix. A nil Event is ordered progress
// without a document change. Consumers save Checkpoint only after all preceding
// required work, including Event when present, succeeds.
type WatchFrame struct {
	Event      *Event
	Checkpoint WatchCheckpoint
}

// WatchStream owns one subscription, not the shared Store connection. Returned
// frames remain valid after subsequent reads. Calls to Next must be serialized;
// Close may interrupt a blocked Next. Read cancellation terminates this stream.
type WatchStream interface {
	// InitialCheckpoint is available when Watch succeeds, including on an idle
	// source. On resume it confirms the exact requested checkpoint.
	InitialCheckpoint() WatchCheckpoint
	// Next returns ordered data/progress or a terminal error. A successful frame
	// always carries a nonempty checkpoint. Errors remain visible on later reads.
	Next(context.Context) (WatchFrame, error)
	// Close cancels this subscription and reports bounded cleanup failures. It is
	// idempotent and leaves other watches and Store operations available.
	Close() error
}

type WatchErrorCode string

const (
	WatchInvalidScope       WatchErrorCode = "INVALID_SCOPE"
	WatchInvalidCheckpoint  WatchErrorCode = "INVALID_CHECKPOINT"
	WatchSourceMismatch     WatchErrorCode = "SOURCE_MISMATCH"
	WatchScopeMismatch      WatchErrorCode = "SCOPE_MISMATCH"
	WatchHistoryUnavailable WatchErrorCode = "HISTORY_UNAVAILABLE"
	WatchPayloadUnavailable WatchErrorCode = "PAYLOAD_UNAVAILABLE"
	WatchInvalidEvent       WatchErrorCode = "INVALID_EVENT"
	WatchUnsupported        WatchErrorCode = "UNSUPPORTED"
	WatchPermissionDenied   WatchErrorCode = "PERMISSION_DENIED"
	WatchSourceUnavailable  WatchErrorCode = "SOURCE_UNAVAILABLE"
)

// WatchError preserves a backend-independent reason and its original cause.
// Cancellation and deadlines remain discoverable through errors.Is.
type WatchError struct {
	Code       WatchErrorCode
	Database   string
	Collection string
	Cause      error
}

func (e *WatchError) Error() string {
	return fmt.Sprintf("watch %s for database %q collection %q: %v", e.Code, e.Database, e.Collection, e.Cause)
}

func (e *WatchError) Unwrap() error { return e.Cause }
