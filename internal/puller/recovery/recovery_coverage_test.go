package recovery

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/checkpoint"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestClassifyMongoErrorPreservesCause(t *testing.T) {
	for _, tc := range []struct {
		native int32
		code   checkpoint.ErrorCode
	}{
		{286, checkpoint.HistoryUnavailable},
		{260, checkpoint.InvalidCheckpoint},
		{280, checkpoint.SourceMismatch},
		{26, checkpoint.SourceMismatch},
	} {
		t.Run(string(tc.code)+fmt.Sprint(tc.native), func(t *testing.T) {
			cause := &mongo.CommandError{Code: tc.native, Message: "native failure"}
			err := ClassifyMongoError(fmt.Errorf("watch: %w", cause))
			var typed *checkpoint.Error
			require.ErrorAs(t, err, &typed)
			require.Equal(t, tc.code, typed.Code)
			require.ErrorIs(t, err, cause)
			require.Equal(t, ActionFatal, NewHandler(HandlerOptions{}).HandleError(err))
		})
	}
}

func TestHandlerTypedFailuresAreFatal(t *testing.T) {
	for _, code := range []checkpoint.ErrorCode{checkpoint.InvalidCheckpoint, checkpoint.IncompatibleState, checkpoint.SourceMismatch, checkpoint.ScopeMismatch, checkpoint.HistoryUnavailable, checkpoint.SourceUnavailable} {
		t.Run(string(code), func(t *testing.T) {
			err := &checkpoint.Error{Code: code, Message: "capture failed", Cause: errors.New("cannot proceed")}
			require.Equal(t, ActionFatal, NewHandler(HandlerOptions{}).HandleError(err))
		})
	}
}

func TestHandlerTransientDiscoveryRetries(t *testing.T) {
	for _, cause := range []error{io.EOF, &mongo.CommandError{Code: 91, Labels: []string{"ResumableChangeStreamError"}}} {
		err := &checkpoint.Error{Code: checkpoint.SourceUnavailable, Message: "lookup failed", Cause: cause}
		require.Equal(t, ActionReconnect, NewHandler(HandlerOptions{}).HandleError(err))
		require.ErrorIs(t, err, cause)
	}
}

func TestNonResumableLabelIsFatal(t *testing.T) {
	cause := &mongo.CommandError{Code: 999, Labels: []string{"NonResumableChangeStreamError"}}
	err := ClassifyMongoError(cause)
	var typed *checkpoint.Error
	require.ErrorAs(t, err, &typed)
	require.Equal(t, checkpoint.SourceUnavailable, typed.Code)
	require.Equal(t, ActionFatal, NewHandler(HandlerOptions{}).HandleError(err))
}

func TestUnlabeledRetryableReadErrorsReconnect(t *testing.T) {
	for _, code := range []int32{91, 10107, 11600, 11602, 13435, 13436, 189, 7, 6, 89, 9001, 262, 43} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			cause := &mongo.CommandError{Code: code, Message: "native read failed"}
			for _, stage := range []string{"metadata", "watch"} {
				t.Run(stage, func(t *testing.T) {
					var err error = fmt.Errorf("open change stream: %w", cause)
					if stage == "metadata" {
						err = &checkpoint.Error{Code: checkpoint.SourceUnavailable, Message: "read collection identities", Cause: cause}
					}
					err = ClassifyMongoError(err)
					handler := NewHandler(HandlerOptions{MaxConsecutiveErrors: 1})
					require.Equal(t, ActionReconnect, handler.HandleError(err))
					require.Equal(t, ActionReconnect, handler.HandleError(err))
					require.ErrorIs(t, err, cause)
				})
			}
		})
	}
}

func TestNativeReadFailureBoundaries(t *testing.T) {
	for _, code := range []int32{13, 90, 92, 10106, 10108, 10058, 11601, 11603, 9000, 9002, 261, 263} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			cause := &mongo.CommandError{Code: code, Message: "native read rejected"}
			err := ClassifyMongoError(cause)
			var typed *checkpoint.Error
			require.ErrorAs(t, err, &typed)
			require.Equal(t, checkpoint.SourceUnavailable, typed.Code)
			require.Equal(t, ActionFatal, NewHandler(HandlerOptions{}).HandleError(err))
			require.ErrorIs(t, err, cause)
		})
	}
}

func TestTerminalNativeErrorsOverrideRetrySignals(t *testing.T) {
	for _, code := range []int32{26, 260, 280, 286} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			cause := &mongo.CommandError{Code: code, Labels: []string{"ResumableChangeStreamError", "NetworkError"}}
			for _, err := range []error{cause, &checkpoint.Error{Code: checkpoint.SourceUnavailable, Message: "read collection identities", Cause: cause}} {
				require.Equal(t, ActionFatal, NewHandler(HandlerOptions{}).HandleError(err))
			}
		})
	}
	for _, code := range []int32{91, 10107, 262, 43} {
		cause := &mongo.CommandError{Code: code, Labels: []string{"NonResumableChangeStreamError", "NetworkError"}}
		err := ClassifyMongoError(cause)
		require.Equal(t, ActionFatal, NewHandler(HandlerOptions{}).HandleError(err))
		require.ErrorIs(t, err, cause)
	}
}
