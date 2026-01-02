package buffer

import (
	"errors"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codetrek/syntrix/internal/puller/events"
)

func TestBuffer_Write_CommitErrorUsesBatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{
		Path:          dir,
		BatchInterval: 5 * time.Millisecond,
	})
	require.NoError(t, err)
	defer buf.Close()

	batchErr := errors.New("commit failed")
	mockBatch := &fakeBatch{commitErr: batchErr}
	buf.newBatch = func() pebbleBatch {
		return mockBatch
	}

	err = buf.Write(&events.ChangeEvent{EventID: "evt-1"}, testToken)
	require.NoError(t, err)

	// Wait for batcher to fail and close buffer
	time.Sleep(20 * time.Millisecond)

	// Buffer should be closed
	err = buf.Write(&events.ChangeEvent{EventID: "evt-2"}, testToken)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "buffer is closed")
}

func TestBuffer_Delete_CommitErrorUsesBatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	buf, err := New(Options{Path: dir})
	require.NoError(t, err)
	defer buf.Close()

	batchErr := errors.New("commit failed")
	mockBatch := &fakeBatch{commitErr: batchErr}
	buf.newBatch = func() pebbleBatch {
		return mockBatch
	}

	err = buf.Delete("evt-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit")
	assert.Equal(t, 1, mockBatch.deleteCalls)
	assert.True(t, mockBatch.closed)
}

type fakeBatch struct {
	setCalls    int
	deleteCalls int
	setErr      error
	deleteErr   error
	commitErr   error
	closed      bool
}

func (f *fakeBatch) Set(key, value []byte, opts *pebble.WriteOptions) error {
	f.setCalls++
	return f.setErr
}

func (f *fakeBatch) Delete(key []byte, opts *pebble.WriteOptions) error {
	f.deleteCalls++
	return f.deleteErr
}

func (f *fakeBatch) Commit(opts *pebble.WriteOptions) error {
	return f.commitErr
}

func (f *fakeBatch) Close() error {
	f.closed = true
	return nil
}
