// Package recovery provides error recovery and gap detection.
package recovery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/mongo"
)

// GapThreshold is the default time gap that triggers a gap detection alert.
const GapThreshold = 5 * time.Minute

// GapDetector detects time gaps in the event stream.
type GapDetector struct {
	threshold time.Duration
	logger    *slog.Logger

	// lastEventTime is the timestamp of the last event
	lastEventTime time.Time

	// gapsDetected counts the number of gaps detected
	gapsDetected int
}

// GapDetectorOptions configures the gap detector.
type GapDetectorOptions struct {
	// Threshold is the minimum gap duration to consider as a gap.
	Threshold time.Duration

	// Logger for gap detection.
	Logger *slog.Logger
}

// NewGapDetector creates a new gap detector.
func NewGapDetector(opts GapDetectorOptions) *GapDetector {
	threshold := opts.Threshold
	if threshold == 0 {
		threshold = GapThreshold
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &GapDetector{
		threshold: threshold,
		logger:    logger.With("component", "gap-detector"),
	}
}

// RecordEvent records an event and checks for gaps.
// Returns true if a gap was detected.
func (g *GapDetector) RecordEvent(evt *events.StoreChangeEvent) bool {
	// Convert cluster time to actual time
	eventTime := time.Unix(int64(evt.ClusterTime.T), 0)

	// Skip if this is the first event
	if g.lastEventTime.IsZero() {
		g.lastEventTime = eventTime
		return false
	}

	// Check for gap
	gap := eventTime.Sub(g.lastEventTime)
	if gap >= g.threshold {
		g.gapsDetected++
		g.logger.Warn("gap detected in event stream",
			"gap", gap.String(),
			"threshold", g.threshold.String(),
			"lastEventTime", g.lastEventTime,
			"currentEventTime", eventTime,
			"eventId", evt.EventID,
		)
		g.lastEventTime = eventTime
		return true
	}

	g.lastEventTime = eventTime
	return false
}

// GapsDetected returns the number of gaps detected.
func (g *GapDetector) GapsDetected() int {
	return g.gapsDetected
}

// Reset resets the detector state.
func (g *GapDetector) Reset() {
	g.lastEventTime = time.Time{}
	g.gapsDetected = 0
}

// Action represents the action to take on error.
type Action int

const (
	ActionNone Action = iota
	ActionReconnect
	ActionFatal
)

func (a Action) String() string {
	switch a {
	case ActionNone:
		return "none"
	case ActionReconnect:
		return "reconnect"
	case ActionFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// Handler retries transient failures while retaining the last admitted source position.
type Handler struct {
	logger               *slog.Logger
	consecutiveErrors    int
	maxConsecutiveErrors int
	resumeTokenErrors    int
}

// HandlerOptions configures the recovery handler.
type HandlerOptions struct {
	MaxConsecutiveErrors int
	Logger               *slog.Logger
}

func NewHandler(opts HandlerOptions) *Handler {
	maxErrors := opts.MaxConsecutiveErrors
	if maxErrors == 0 {
		maxErrors = 10
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		logger:               logger.With("component", "recovery-handler"),
		maxConsecutiveErrors: maxErrors,
	}
}

// ClassifyMongoError preserves the native cause and gives non-resumable positions
// a stable failure code. No classified failure permits discarding a checkpoint.
func ClassifyMongoError(err error) error {
	if err == nil {
		return nil
	}
	var sourceErr *checkpoint.Error
	if errors.As(err, &sourceErr) {
		return err
	}
	var serverErr mongo.ServerError
	if errors.As(err, &serverErr) {
		switch {
		case serverErr.HasErrorCode(286):
			return &checkpoint.Error{Code: checkpoint.HistoryUnavailable, Message: "MongoDB change stream history is unavailable", Cause: err}
		case serverErr.HasErrorCode(260):
			return &checkpoint.Error{Code: checkpoint.InvalidCheckpoint, Message: "MongoDB rejected the resume token", Cause: err}
		case serverErr.HasErrorCode(280), serverErr.HasErrorCode(26):
			return &checkpoint.Error{Code: checkpoint.SourceMismatch, Message: "MongoDB change stream source is no longer resumable", Cause: err}
		case serverErr.HasErrorLabel("NonResumableChangeStreamError"):
			return &checkpoint.Error{Code: checkpoint.SourceUnavailable, Message: "MongoDB change stream cannot resume", Cause: err}
		}
		if !isNativeTransientError(err) {
			return &checkpoint.Error{Code: checkpoint.SourceUnavailable, Message: "MongoDB rejected the change stream operation", Cause: err}
		}
	}
	if isResumeTokenError(err) {
		return &checkpoint.Error{Code: checkpoint.HistoryUnavailable, Message: "MongoDB change stream position is unavailable", Cause: err}
	}
	return err
}

func (h *Handler) HandleError(err error) Action {
	if err == nil {
		h.consecutiveErrors = 0
		return ActionNone
	}
	h.consecutiveErrors++
	err = ClassifyMongoError(err)
	var sourceErr *checkpoint.Error
	if errors.As(err, &sourceErr) {
		if sourceErr.Code == checkpoint.SourceUnavailable && isNativeTransientError(sourceErr.Cause) {
			return ActionReconnect
		}
		if sourceErr.Code == checkpoint.HistoryUnavailable || sourceErr.Code == checkpoint.InvalidCheckpoint {
			h.resumeTokenErrors++
		}
		h.logger.Error("capture cannot resume", "error", err)
		return ActionFatal
	}
	if isTransientError(err) {
		return ActionReconnect
	}
	if h.consecutiveErrors >= h.maxConsecutiveErrors {
		h.logger.Error("max consecutive errors reached", "error", err, "count", h.consecutiveErrors)
		return ActionFatal
	}
	return ActionReconnect
}

func (h *Handler) ResetErrorCount()       { h.consecutiveErrors = 0 }
func (h *Handler) ResumeTokenErrors() int { return h.resumeTokenErrors }

func isResumeTokenError(err error) bool {
	for _, msg := range []string{
		"resume token was not found",
		"resume point may no longer be in the oplog",
		"ChangeStreamHistoryLost",
		"ChangeStreamFatalError",
	} {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(msg)) {
			return true
		}
	}
	return false
}

func isNativeTransientError(err error) bool {
	if err == nil {
		return false
	}
	var serverErr mongo.ServerError
	if errors.As(err, &serverErr) {
		if serverErr.HasErrorLabel("NonResumableChangeStreamError") || serverErr.HasErrorCode(26) || serverErr.HasErrorCode(260) || serverErr.HasErrorCode(280) || serverErr.HasErrorCode(286) {
			return false
		}
		if serverErr.HasErrorLabel("ResumableChangeStreamError") || serverErr.HasErrorCode(43) {
			return true
		}
		// Metadata reads and initial aggregate failures need not carry a change-
		// stream label. Preserve the driver's retryable-read code classification.
		// https://github.com/mongodb/mongo-go-driver/blob/d2fa0ab6f3ba0579b7bca7912d30e23907ffec9a/x/mongo/driver/errors.go#L28
		for _, code := range []int{11600, 11602, 10107, 13435, 13436, 189, 91, 7, 6, 89, 9001, 262} {
			if serverErr.HasErrorCode(code) {
				return true
			}
		}
	}
	if mongo.IsNetworkError(err) || mongo.IsTimeout(err) || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

func isTransientError(err error) bool {
	if isNativeTransientError(err) {
		return true
	}
	for _, msg := range []string{"connection reset", "connection refused", "broken pipe", "EOF", "timeout", "context deadline exceeded", "network", "temporary failure"} {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(msg)) {
			return true
		}
	}
	return false
}
