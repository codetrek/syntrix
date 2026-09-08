package buffer

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/syntrixbase/syntrix/internal/puller/events"
)

func TestBuffer_Write_CommitFailureStopsPendingBatch(t *testing.T) {
	t.Parallel()
	for _, closeDuringCommit := range []bool{false, true} {
		name := "background failure"
		if closeDuringCommit {
			name = "close during failure"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			buf, err := New(Options{Path: dir, BatchSize: 1, BatchInterval: time.Hour})
			require.NoError(t, err)

			token0, err := bson.Marshal(bson.M{"position": 0})
			require.NoError(t, err)
			require.NoError(t, buf.SaveCheckpoint(token0))
			token1, err := bson.Marshal(bson.M{"position": 1})
			require.NoError(t, err)
			token2, err := bson.Marshal(bson.M{"position": 2})
			require.NoError(t, err)

			commitStarted := make(chan struct{})
			releaseCommit := make(chan struct{})
			var releaseOnce sync.Once
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(releaseCommit) })
				buf.Close()
			})
			batchErr := errors.New("first batch commit failed")
			batchCount := 0
			buf.newBatch = func() pebbleBatch {
				batchCount++
				if batchCount > 1 {
					return buf.db.NewBatch()
				}
				return &fakeBatch{commit: func() error {
					close(commitStarted)
					<-releaseCommit
					return batchErr
				}}
			}

			require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-1"}, token1))
			select {
			case <-commitStarted:
			case <-time.After(5 * time.Second):
				t.Fatal("first batch did not reach commit")
			}
			require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-2"}, token2))

			closeResults := make(chan error, 2)
			if closeDuringCommit {
				for range 2 {
					go func() { closeResults <- buf.Close() }()
				}
				select {
				case <-buf.closeCh:
				case <-time.After(5 * time.Second):
					t.Fatal("Close did not signal the batcher")
				}
			}
			releaseOnce.Do(func() { close(releaseCommit) })
			batcherDone := make(chan struct{})
			go func() {
				buf.batcherWG.Wait()
				close(batcherDone)
			}()
			select {
			case <-batcherDone:
			case <-time.After(5 * time.Second):
				t.Fatal("batcher did not stop after commit failed")
			}

			assert.Equal(t, 1, batchCount)
			assert.ErrorIs(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-3"}, token2), batchErr)
			_, err = buf.LoadCheckpoint()
			assert.ErrorIs(t, err, batchErr)
			if closeDuringCommit {
				for range 2 {
					select {
					case err := <-closeResults:
						assert.ErrorIs(t, err, batchErr)
					case <-time.After(5 * time.Second):
						t.Fatal("Close did not return after batch failure")
					}
				}
			}
			assert.ErrorIs(t, buf.Close(), batchErr)
			assert.ErrorIs(t, buf.Close(), batchErr)

			reopened, err := New(Options{Path: dir})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, reopened.Close()) })
			checkpoint, err := reopened.LoadCheckpoint()
			require.NoError(t, err)
			assert.Equal(t, bson.Raw(token0), checkpoint)
			count, err := reopened.Count()
			require.NoError(t, err)
			assert.Zero(t, count)
		})
	}
}

func TestBuffer_Write_BatchErrorsRemainObservable(t *testing.T) {
	t.Parallel()
	batchErr := errors.New("batch failure")
	for name, mockBatch := range map[string]*fakeBatch{
		"event set":      {setErr: batchErr, setErrorAt: 1},
		"checkpoint set": {setErr: batchErr, setErrorAt: 2},
		"close":          {closeErr: batchErr},
		"first error":    {commitErr: batchErr, closeErr: errors.New("later close failure")},
	} {
		t.Run(name, func(t *testing.T) {
			buf, err := New(Options{Path: t.TempDir(), BatchSize: 1, BatchInterval: time.Hour})
			require.NoError(t, err)
			t.Cleanup(func() { buf.Close() })
			buf.newBatch = func() pebbleBatch { return mockBatch }
			require.NoError(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-1"}, testToken))
			select {
			case <-buf.closeCh:
			case <-time.After(5 * time.Second):
				t.Fatal("batcher did not report failure")
			}
			assert.ErrorIs(t, buf.Close(), batchErr)
			assert.ErrorIs(t, buf.Close(), batchErr)
			assert.ErrorIs(t, buf.Write(&events.StoreChangeEvent{EventID: "evt-2"}, testToken), batchErr)
			_, err = buf.LoadCheckpoint()
			assert.ErrorIs(t, err, batchErr)
			assert.True(t, mockBatch.closed)
		})
	}
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
	setErrorAt  int
	deleteErr   error
	commitErr   error
	commit      func() error
	closeErr    error
	closed      bool
}

func (f *fakeBatch) Set(key, value []byte, opts *pebble.WriteOptions) error {
	f.setCalls++
	if f.setErrorAt == 0 || f.setCalls == f.setErrorAt {
		return f.setErr
	}
	return nil
}

func (f *fakeBatch) Delete(key []byte, opts *pebble.WriteOptions) error {
	f.deleteCalls++
	return f.deleteErr
}

func (f *fakeBatch) Commit(opts *pebble.WriteOptions) error {
	if f.commit != nil {
		return f.commit()
	}
	return f.commitErr
}

func (f *fakeBatch) Close() error {
	f.closed = true
	return f.closeErr
}
