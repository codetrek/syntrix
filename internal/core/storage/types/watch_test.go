package types

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWatchErrorPreservesReasonScopeAndCause(t *testing.T) {
	failure := &WatchError{
		Code: WatchSourceUnavailable, Database: "database", Collection: "users",
		Cause: fmt.Errorf("read interrupted: %w", context.DeadlineExceeded),
	}
	err := fmt.Errorf("consumer: %w", failure)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var watchErr *WatchError
	require.ErrorAs(t, err, &watchErr)
	require.Same(t, failure, watchErr)
	require.Equal(t, WatchSourceUnavailable, watchErr.Code)
	require.Contains(t, err.Error(), `database "database" collection "users"`)
	require.Contains(t, err.Error(), "read interrupted")
	require.False(t, errors.Is(err, context.Canceled))
}
