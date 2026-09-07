// Package recovery classifies source failures without changing persisted progress.
package recovery

import (
	"context"
	"errors"
	"io"
	"net"

	"go.mongodb.org/mongo-driver/mongo"
)

// IsHistoryLost reports failures that prevent proving continuity from the saved
// token. The caller must preserve that token and mark the source discontinuous.
func IsHistoryLost(err error) bool {
	var serverErr mongo.ServerError
	if !errors.As(err, &serverErr) {
		return false
	}

	// MongoDB defines InvalidResumeToken, ChangeStreamFatalError and
	// ChangeStreamHistoryLost as codes 260, 280 and 286 respectively:
	// https://github.com/mongodb/mongo/blob/b41cda4fe697dce6fd9b83b3805362ccc02fbeb3/src/mongo/base/error_codes.yml#L312-L342
	return serverErr.HasErrorCode(260) ||
		serverErr.HasErrorCode(280) ||
		serverErr.HasErrorCode(286) ||
		serverErr.HasErrorLabel("NonResumableChangeStreamError")
}

// IsRetryable reports failures for which reconnecting at the saved token is
// justified. The caller must also check its context before retrying: an operation
// timeout can be retried, but expiration of the backend's lifetime cannot.
func IsRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || IsHistoryLost(err) {
		return false
	}
	if mongo.IsNetworkError(err) || mongo.IsTimeout(err) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true
	}

	// CursorNotFound is resumable even without a label because the server no
	// longer knows that the missing cursor belonged to a change stream:
	// https://github.com/mongodb/mongo-go-driver/blob/d2fa0ab6f3ba0579b7bca7912d30e23907ffec9a/mongo/change_stream.go#L698-L715
	var serverErr mongo.ServerError
	return errors.As(err, &serverErr) &&
		(serverErr.HasErrorCode(43) || serverErr.HasErrorLabel("ResumableChangeStreamError"))
}
