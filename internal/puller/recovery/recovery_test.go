package recovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestSourceFailureClassification(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		historyLost bool
		retryable   bool
	}{
		{name: "no error"},
		{name: "invalid resume token", err: mongo.CommandError{Code: 260}, historyLost: true},
		{name: "fatal change stream", err: mongo.CommandError{Code: 280}, historyLost: true},
		{name: "expired history", err: mongo.CommandError{Code: 286}, historyLost: true},
		{name: "nonresumable label", err: mongo.CommandError{Code: 999, Labels: []string{"NonResumableChangeStreamError"}}, historyLost: true},
		{name: "history loss overrides network label", err: mongo.CommandError{Code: 286, Labels: []string{"NetworkError", "ResumableChangeStreamError"}}, historyLost: true},
		{name: "server interface", err: mongo.WriteException{WriteErrors: mongo.WriteErrors{{Code: 286}}}, historyLost: true},
		{name: "missing cursor", err: mongo.CommandError{Code: 43}, retryable: true},
		{name: "resumable label", err: mongo.CommandError{Code: 999, Labels: []string{"ResumableChangeStreamError"}}, retryable: true},
		{name: "driver network error", err: mongo.CommandError{Labels: []string{"NetworkError"}}, retryable: true},
		{name: "driver timeout", err: mongo.CommandError{Code: 50}, retryable: true},
		{name: "connection reset", err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, retryable: true},
		{name: "timeout", err: &net.DNSError{IsTimeout: true}, retryable: true},
		{name: "end of stream", err: io.EOF, retryable: true},
		{name: "truncated stream", err: io.ErrUnexpectedEOF, retryable: true},
		{name: "operation deadline", err: context.DeadlineExceeded, retryable: true},
		{name: "cancellation", err: context.Canceled},
		{name: "network cancellation", err: &net.OpError{Op: "read", Err: context.Canceled}},
		{name: "unknown failure", err: errors.New("unexpected storage state")},
		{name: "authentication", err: mongo.CommandError{Code: 18}},
		{name: "unlabeled server failure", err: mongo.CommandError{Code: 91}},
		{name: "network words", err: errors.New("network timeout, connection reset, EOF")},
		{name: "history words", err: errors.New("ChangeStreamHistoryLost: resume token was not found")},
		{name: "server message is not a code", err: mongo.CommandError{Code: 18, Message: "ChangeStreamHistoryLost timeout"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.historyLost, IsHistoryLost(test.err))
			require.Equal(t, test.retryable, IsRetryable(test.err))
			if test.err != nil {
				wrapped := fmt.Errorf("read source: %w", test.err)
				require.Equal(t, test.historyLost, IsHistoryLost(wrapped), "wrapped history classification")
				require.Equal(t, test.retryable, IsRetryable(wrapped), "wrapped retry classification")
			}
		})
	}
}
