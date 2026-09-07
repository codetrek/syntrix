package events

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDomainErrorRetainsRecoveryMetadataAndCause(t *testing.T) {
	cause := errors.New("synchronization failed")
	err := &Error{
		Code: CodeStorageFailure, Backend: "mongo", SourceID: "source-a",
		Generation: "generation-1", DiscardedThrough: 10, CommittedThrough: 20,
		Cause: fmt.Errorf("commit event batch: %w", cause),
	}
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, uint64(10), err.DiscardedThrough)
	assert.Equal(t, uint64(20), err.CommittedThrough)
	assert.Contains(t, err.Error(), "STORAGE_FAILURE")
	assert.Contains(t, err.Error(), `backend="mongo"`)
	assert.Contains(t, err.Error(), `source="source-a"`)
	assert.Contains(t, err.Error(), `generation="generation-1"`)
	assert.Contains(t, err.Error(), "commit event batch: synchronization failed")
}

func TestDomainErrorWithoutOptionalMetadata(t *testing.T) {
	err := &Error{Code: CodeInvalidCursor}
	assert.Equal(t, "INVALID_CURSOR", err.Error())
	assert.Nil(t, errors.Unwrap(err))
	err.SourceID = "source\nname"
	assert.Contains(t, err.Error(), `source="source\nname"`)
}
