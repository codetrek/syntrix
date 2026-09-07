package buffer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func testOptions(path string) Options {
	return Options{Path: path, SourceID: "source-a", SourceScope: "database/collection", BatchSize: 1, BatchInterval: 20 * time.Millisecond, QueueSize: 10, QueueBytes: 1 << 20, BatchBytes: 1 << 18}
}
func testEvent(id string) *events.StoreChangeEvent {
	return &events.StoreChangeEvent{MgoColl: "documents", MgoDocID: id, OpType: events.StoreOperationInsert, Timestamp: time.Now().UnixMilli(), ClusterTime: events.ClusterTime{T: 1, I: 1}, FullDocument: &storage.StoredDoc{Id: id, Data: map[string]any{"value": id}}}
}
func testToken(t *testing.T, id string) bson.Raw {
	t.Helper()
	token, err := bson.Marshal(bson.D{{Key: "_data", Value: id}})
	require.NoError(t, err)
	return token
}
func closeBuffer(t *testing.T, b *Buffer) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, b.Close(ctx))
}
func committed(t *testing.T, ch <-chan CommittedBatch) CommittedBatch {
	t.Helper()
	select {
	case batch := <-ch:
		return batch
	case <-time.After(5 * time.Second):
		t.Fatal("commit callback timed out")
		return CommittedBatch{}
	}
}
func assertCode(t *testing.T, err error, code events.ErrorCode) {
	t.Helper()
	var domainErr *events.Error
	require.ErrorAs(t, err, &domainErr)
	require.Equal(t, code, domainErr.Code)
}

type controlledBatch struct {
	pebbleBatch
	commit func(pebbleBatch, *pebble.WriteOptions) error
}

func (b *controlledBatch) Commit(options *pebble.WriteOptions) error {
	return b.commit(b.pebbleBatch, options)
}
func controlledFactory(hook func(pebbleBatch, *pebble.WriteOptions) error) func(*pebble.DB) pebbleBatch {
	var calls atomic.Int32
	return func(db *pebble.DB) pebbleBatch {
		batch := db.NewBatch()
		if calls.Add(1) == 1 {
			return batch
		}
		return &controlledBatch{pebbleBatch: batch, commit: hook}
	}
}

func TestVisibilityWaitsForSyncCompletionAndFreezesInput(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.QueueSize = 1
	visible, release := make(chan struct{}), make(chan struct{})
	opts.newBatch = controlledFactory(func(batch pebbleBatch, options *pebble.WriteOptions) error {
		err := batch.Commit(options)
		close(visible)
		<-release
		return err
	})
	callbacks := make(chan CommittedBatch, 1)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	b, err := New(opts)
	require.NoError(t, err)
	initial, err := b.State()
	require.NoError(t, err)
	event, token := testEvent("original"), testToken(t, "original")
	originalToken := append(bson.Raw(nil), token...)
	require.NoError(t, b.Enqueue(context.Background(), event, token))
	event.FullDocument.Data["value"] = "mutated"
	event.MgoDocID = "mutated"
	token[5] ^= 1
	select {
	case <-visible:
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not reach visibility")
	}
	state, err := b.State()
	require.NoError(t, err)
	require.Equal(t, initial, state)
	_, closer, err := b.db.Get(eventKey(initial.Position.Generation, 1))
	require.NoError(t, err)
	require.NoError(t, closer.Close())
	future := initial.Position
	future.Sequence = 1
	_, err = b.ReadPage(context.Background(), initial.Position, future, 10, 1<<16)
	assertCode(t, err, events.CodePositionAhead)
	select {
	case <-callbacks:
		t.Fatal("unconfirmed batch was published")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	err = b.Enqueue(ctx, testEvent("next"), testToken(t, "next"))
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	err = b.Close(ctx)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// A deadline cannot close the DB while its worker still owns a commit.
	_, lockErr := pebble.Open(opts.Path, &pebble.Options{})
	require.Error(t, lockErr)
	close(release)
	batch := committed(t, callbacks)
	require.Len(t, batch.Records, 1)
	require.Equal(t, "original", batch.Records[0].Event.FullDocument.Data["value"])
	require.Equal(t, "original", batch.Records[0].Event.MgoDocID)
	require.Equal(t, originalToken, batch.State.ResumeToken)
	closeBuffer(t, b)
	reopened, err := New(testOptions(opts.Path))
	require.NoError(t, err)
	defer closeBuffer(t, reopened)
	state, err = reopened.State()
	require.NoError(t, err)
	page, err := reopened.ReadPage(context.Background(), initial.Position, state.Position, 10, 1<<16)
	require.NoError(t, err)
	require.Equal(t, batch.Records, page.Records)
}

func TestAdmissionBytesIncludeInFlightBatchAndCallback(t *testing.T) {
	opts := testOptions(t.TempDir())
	entered, release := make(chan struct{}), make(chan struct{})
	opts.OnCommit = func(CommittedBatch) { close(entered); <-release }
	b, err := New(opts)
	require.NoError(t, err)
	event, token := testEvent("one"), testToken(t, "one")
	b.mu.Lock()
	request, err := b.freeze(event, token)
	b.mu.Unlock()
	require.NoError(t, err)
	b.mu.Lock()
	b.opts.QueueBytes = request.credit*2 - 1
	b.opts.BatchBytes = request.credit
	b.mu.Unlock()
	require.NoError(t, b.Enqueue(context.Background(), event, token))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("callback not reached")
	}
	b.mu.RLock()
	require.Equal(t, request.credit, b.bytes)
	require.Equal(t, 1, b.count)
	b.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	require.ErrorIs(t, b.Enqueue(ctx, event, token), context.DeadlineExceeded)
	cancel()
	oversized := testEvent("large")
	oversized.FullDocument.Data["value"] = strings.Repeat("x", int(request.credit))
	err = b.Enqueue(context.Background(), oversized, token)
	assertCode(t, err, events.CodeOverloaded)
	close(release)
	closeBuffer(t, b)
}

func TestBatchLimitsDrainAndDeduplicateWithoutRegressingToken(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.BatchSize = 2
	opts.BatchInterval = time.Hour
	callbacks := make(chan CommittedBatch, 10)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	b, err := New(opts)
	require.NoError(t, err)
	initial, err := b.State()
	require.NoError(t, err)
	for _, id := range []string{"z", "z", "a", "b", "a"} {
		require.NoError(t, b.Enqueue(context.Background(), testEvent(id), testToken(t, id)))
	}
	closeBuffer(t, b)
	close(callbacks)
	var all []Record
	for batch := range callbacks {
		require.LessOrEqual(t, len(batch.Records), 2)
		all = append(all, batch.Records...)
	}
	require.Len(t, all, 3)
	require.Equal(t, []string{"z", "a", "b"}, []string{all[0].Event.MgoDocID, all[1].Event.MgoDocID, all[2].Event.MgoDocID})
	for i, record := range all {
		require.Equal(t, uint64(i+1), record.Position.Sequence)
	}
	reopened, err := New(testOptions(opts.Path))
	require.NoError(t, err)
	defer closeBuffer(t, reopened)
	state, err := reopened.State()
	require.NoError(t, err)
	require.Equal(t, testToken(t, "b"), state.ResumeToken)
	page, err := reopened.ReadPage(context.Background(), initial.Position, state.Position, 1, 1<<16)
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	require.Equal(t, uint64(1), page.Through.Sequence)
	next, err := reopened.ReadPage(context.Background(), page.Through, state.Position, 10, page.Records[0].EncodedBytes)
	require.NoError(t, err)
	require.Len(t, next.Records, 1)
	_, err = reopened.ReadPage(context.Background(), initial.Position, state.Position, 10, 1)
	assertCode(t, err, events.CodeOverloaded)
}

func TestCommitFailureWakesAdmissionAndPreservesCause(t *testing.T) {
	failure := errors.New("injected disk failure")
	opts := testOptions(t.TempDir())
	opts.QueueSize = 1
	entered, release := make(chan struct{}), make(chan struct{})
	opts.newBatch = controlledFactory(func(pebbleBatch, *pebble.WriteOptions) error { close(entered); <-release; return failure })
	failures := make(chan error, 1)
	opts.OnError = func(err error) { failures <- err }
	var published atomic.Int32
	opts.OnCommit = func(CommittedBatch) { published.Add(1) }
	b, err := New(opts)
	require.NoError(t, err)
	require.NoError(t, b.Enqueue(context.Background(), testEvent("one"), testToken(t, "one")))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("commit did not start")
	}
	waiter := make(chan error, 1)
	go func() { waiter <- b.Enqueue(context.Background(), testEvent("two"), testToken(t, "two")) }()
	close(release)
	select {
	case err := <-waiter:
		require.ErrorIs(t, err, failure)
	case <-time.After(5 * time.Second):
		t.Fatal("blocked admission did not wake")
	}
	select {
	case err := <-failures:
		require.ErrorIs(t, err, failure)
	case <-time.After(5 * time.Second):
		t.Fatal("failure callback missing")
	}
	require.ErrorIs(t, b.Close(context.Background()), failure)
	require.ErrorIs(t, b.Close(context.Background()), failure)
	require.Zero(t, published.Load())
	reopened, err := New(testOptions(opts.Path))
	require.NoError(t, err)
	defer closeBuffer(t, reopened)
	state, err := reopened.State()
	require.NoError(t, err)
	require.Zero(t, state.Position.Sequence)
	require.Empty(t, state.ResumeToken)
}

func TestRetentionAtomicallyMaintainsFloorIdentityAndLogicalBytes(t *testing.T) {
	opts := testOptions(t.TempDir())
	callbacks := make(chan CommittedBatch, 10)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	b, err := New(opts)
	require.NoError(t, err)
	initial, err := b.State()
	require.NoError(t, err)
	now := time.Now()
	for i, id := range []string{"a", "b", "c"} {
		event := testEvent(id)
		if i < 2 {
			event.Timestamp = now.Add(-time.Hour).UnixMilli()
		}
		require.NoError(t, b.Enqueue(context.Background(), event, testToken(t, id)))
		committed(t, callbacks)
	}
	require.NoError(t, b.Retain(context.Background(), now.Add(-time.Minute), 0))
	state, err := b.State()
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.DiscardedThrough)
	require.Positive(t, state.RetainedBytes)
	_, err = b.ReadPage(context.Background(), initial.Position, state.Position, 10, 1<<16)
	assertCode(t, err, events.CodeHistoryExpired)
	start := initial.Position
	start.Sequence = 2
	page, err := b.ReadPage(context.Background(), start, state.Position, 10, 1<<16)
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	require.Equal(t, "c", page.Records[0].Event.MgoDocID)
	_, closer, err := b.db.Get(identityKey(initial.Position.Generation, eventIdentity(opts.SourceID, testToken(t, "a"))))
	require.ErrorIs(t, err, pebble.ErrNotFound)
	require.Nil(t, closer)
	require.NoError(t, b.Retain(context.Background(), time.Time{}, 1))
	state, err = b.State()
	require.NoError(t, err)
	require.Equal(t, uint64(3), state.DiscardedThrough)
	require.Zero(t, state.RetainedBytes)
	require.Equal(t, testToken(t, "c"), state.ResumeToken)
	closeBuffer(t, b)
	reopened, err := New(testOptions(opts.Path))
	require.NoError(t, err)
	defer closeBuffer(t, reopened)
	restored, err := reopened.State()
	require.NoError(t, err)
	require.Equal(t, state, restored)
}

func TestExplicitFormatSourceAndContinuityBoundaries(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		opts := testOptions(t.TempDir())
		db, err := pebble.Open(opts.Path, &pebble.Options{})
		require.NoError(t, err)
		require.NoError(t, db.Set([]byte("old-event-key"), []byte("event"), pebble.Sync))
		require.NoError(t, db.Close())
		_, err = New(opts)
		assertCode(t, err, events.CodeUnsupportedFormat)
	})
	t.Run("identity and scope", func(t *testing.T) {
		opts := testOptions(t.TempDir())
		b, err := New(opts)
		require.NoError(t, err)
		closeBuffer(t, b)
		changed := opts
		changed.SourceID = "different"
		_, err = New(changed)
		assertCode(t, err, events.CodeUnknownSource)
		changed = opts
		changed.SourceScope = "other/scope"
		_, err = New(changed)
		assertCode(t, err, events.CodeUnknownSource)
	})
	t.Run("discontinuity survives restart", func(t *testing.T) {
		opts := testOptions(t.TempDir())
		b, err := New(opts)
		require.NoError(t, err)
		cause := errors.New("resume history unavailable")
		err = b.MarkDiscontinuous(context.Background(), cause)
		assertCode(t, err, events.CodeContinuityLost)
		require.ErrorIs(t, err, cause)
		require.ErrorIs(t, b.Close(context.Background()), cause)
		_, err = New(opts)
		assertCode(t, err, events.CodeContinuityLost)
	})
	t.Run("invalid frontier", func(t *testing.T) {
		opts := testOptions(t.TempDir())
		b, err := New(opts)
		require.NoError(t, err)
		state, err := b.State()
		require.NoError(t, err)
		closeBuffer(t, b)
		state.Position.Sequence = 1
		state.ResumeToken = testToken(t, "missing")
		db, err := pebble.Open(opts.Path, &pebble.Options{})
		require.NoError(t, err)
		value, err := json.Marshal(diskState{Version: formatVersion, SourceScope: opts.SourceScope, State: state})
		require.NoError(t, err)
		require.NoError(t, db.Set(stateKey, value, pebble.Sync))
		require.NoError(t, db.Close())
		_, err = New(opts)
		assertCode(t, err, events.CodeStorageFailure)
	})
}

func TestCrashRecoveryAtCommitBoundaries(t *testing.T) {
	if path := os.Getenv("SYNTRIX_BUFFER_CRASH_PATH"); path != "" {
		opts := testOptions(path)
		point := os.Getenv("SYNTRIX_BUFFER_CRASH_POINT")
		opts.newBatch = controlledFactory(func(batch pebbleBatch, options *pebble.WriteOptions) error {
			if point == "before" {
				os.Exit(42)
			}
			err := batch.Commit(options)
			if err != nil {
				os.Exit(43)
			}
			if point == "after" {
				os.Exit(42)
			}
			return err
		})
		opts.OnCommit = func(CommittedBatch) { os.Exit(42) }
		b, err := New(opts)
		if err != nil {
			os.Exit(44)
		}
		if err := b.Enqueue(context.Background(), testEvent("crash"), testToken(t, "crash")); err != nil {
			os.Exit(45)
		}
		<-b.Done()
		os.Exit(46)
	}
	for _, point := range []string{"before", "after", "published"} {
		t.Run(point, func(t *testing.T) {
			path := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashRecoveryAtCommitBoundaries$")
			child.Env = append(os.Environ(), "SYNTRIX_BUFFER_CRASH_PATH="+path, "SYNTRIX_BUFFER_CRASH_POINT="+point)
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, string(output))
			require.Equal(t, 42, exit.ExitCode(), string(output))
			b, err := New(testOptions(path))
			require.NoError(t, err)
			defer closeBuffer(t, b)
			state, err := b.State()
			require.NoError(t, err)
			if point == "before" {
				require.Zero(t, state.Position.Sequence)
				require.Empty(t, state.ResumeToken)
				return
			}
			require.Equal(t, uint64(1), state.Position.Sequence)
			require.Equal(t, testToken(t, "crash"), state.ResumeToken)
			start := state.Position
			start.Sequence = 0
			page, err := b.ReadPage(context.Background(), start, state.Position, 10, 1<<16)
			require.NoError(t, err)
			require.Len(t, page.Records, 1)
		})
	}
}

func TestPositionValidationAndCanceledRead(t *testing.T) {
	opts := testOptions(t.TempDir())
	b, err := New(opts)
	require.NoError(t, err)
	defer closeBuffer(t, b)
	state, err := b.State()
	require.NoError(t, err)
	for _, check := range []struct {
		position cursor.Position
		code     events.ErrorCode
	}{
		{cursor.Position{SourceID: "other", Generation: state.Position.Generation}, events.CodeUnknownSource},
		{cursor.Position{SourceID: opts.SourceID, Generation: "other"}, events.CodeGenerationMismatch},
		{cursor.Position{SourceID: opts.SourceID, Generation: state.Position.Generation, Sequence: 1}, events.CodePositionAhead},
	} {
		_, err := b.ReadPage(context.Background(), check.position, state.Position, 1, 1000)
		assertCode(t, err, check.code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = b.ReadPage(ctx, state.Position, state.Position, 1, 1000)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, b.Enqueue(ctx, testEvent("canceled"), testToken(t, "canceled")), context.Canceled)
	page, err := b.ReadPage(context.Background(), state.Position, state.Position, 1, 1000)
	require.NoError(t, err)
	assert.Empty(t, page.Records)
}

func TestRetentionVisibilityWaitsForConfirmation(t *testing.T) {
	opts := testOptions(t.TempDir())
	callbacks := make(chan CommittedBatch, 4)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	visible, release := make(chan struct{}), make(chan struct{})
	var commits atomic.Int32
	opts.newBatch = controlledFactory(func(batch pebbleBatch, options *pebble.WriteOptions) error {
		err := batch.Commit(options)
		if commits.Add(1) == 3 {
			close(visible)
			<-release
		}
		return err
	})
	b, err := New(opts)
	require.NoError(t, err)
	initial, err := b.State()
	require.NoError(t, err)
	event := testEvent("old")
	event.Timestamp = time.Now().Add(-time.Hour).UnixMilli()
	require.NoError(t, b.Enqueue(context.Background(), event, testToken(t, "old")))
	committed(t, callbacks)
	require.NoError(t, b.Enqueue(context.Background(), testEvent("new"), testToken(t, "new")))
	head := committed(t, callbacks).State
	result := make(chan error, 1)
	go func() { result <- b.Retain(context.Background(), time.Now().Add(-time.Minute), 0) }()
	select {
	case <-visible:
	case <-time.After(5 * time.Second):
		t.Fatal("retention did not reach visibility")
	}
	_, closer, err := b.db.Get(eventKey(initial.Position.Generation, 1))
	require.ErrorIs(t, err, pebble.ErrNotFound)
	require.Nil(t, closer)
	state, err := b.State()
	require.NoError(t, err)
	require.Zero(t, state.DiscardedThrough)
	page, err := b.ReadPage(context.Background(), initial.Position, head.Position, 10, 1<<16)
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	close(release)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("retention did not complete")
	}
	_, err = b.ReadPage(context.Background(), initial.Position, head.Position, 10, 1<<16)
	assertCode(t, err, events.CodeHistoryExpired)
	closeBuffer(t, b)
}

func TestCommitErrorAfterPersistenceHasNoPublicationAndRecovers(t *testing.T) {
	opts := testOptions(t.TempDir())
	failure := errors.New("commit completion is ambiguous")
	opts.newBatch = controlledFactory(func(batch pebbleBatch, options *pebble.WriteOptions) error {
		if err := batch.Commit(options); err != nil {
			return err
		}
		return failure
	})
	var published atomic.Int32
	opts.OnCommit = func(CommittedBatch) { published.Add(1) }
	b, err := New(opts)
	require.NoError(t, err)
	require.NoError(t, b.Enqueue(context.Background(), testEvent("persisted"), testToken(t, "persisted")))
	require.ErrorIs(t, b.Close(context.Background()), failure)
	require.Zero(t, published.Load())
	reopened, err := New(testOptions(opts.Path))
	require.NoError(t, err)
	state, err := reopened.State()
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.Position.Sequence)
	token, err := reopened.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, testToken(t, "persisted"), token)
	token[5] ^= 1
	unchanged, err := reopened.LoadCheckpoint()
	require.NoError(t, err)
	require.Equal(t, state.ResumeToken, unchanged)
	require.NoError(t, reopened.Retain(context.Background(), time.Time{}, 1))
	require.NoError(t, reopened.Enqueue(context.Background(), testEvent("duplicate"), testToken(t, "persisted")))
	closeBuffer(t, reopened)
	reopened, err = New(testOptions(opts.Path))
	require.NoError(t, err)
	defer closeBuffer(t, reopened)
	state, err = reopened.State()
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.Position.Sequence)
	require.Equal(t, uint64(1), state.DiscardedThrough)
}

func TestOldestEventDeadlineIsNotResetByIncomingTraffic(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.BatchSize = 1000
	opts.QueueSize = 1000
	opts.BatchInterval = 30 * time.Millisecond
	callbacks := make(chan CommittedBatch, 100)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	b, err := New(opts)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	producer := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				producer <- nil
				return
			case now := <-ticker.C:
				token, err := bson.Marshal(bson.D{{Key: "_data", Value: now.UnixNano()}})
				if err != nil {
					producer <- err
					return
				}
				if err := b.Enqueue(ctx, testEvent("continuous"), token); err != nil && !errors.Is(err, context.Canceled) {
					producer <- err
					return
				}
			}
		}
	}()
	select {
	case batch := <-callbacks:
		require.NotEmpty(t, batch.Records)
		require.Less(t, len(batch.Records), opts.BatchSize)
	case <-time.After(500 * time.Millisecond):
		t.Error("incoming traffic postponed the oldest event deadline")
	}
	cancel()
	select {
	case err := <-producer:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not stop")
	}
	closeBuffer(t, b)
}

func TestBatchByteLimitSplitsPendingWork(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.BatchSize = 100
	opts.BatchInterval = time.Hour
	callbacks := make(chan CommittedBatch, 10)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	b, err := New(opts)
	require.NoError(t, err)
	b.mu.Lock()
	request, err := b.freeze(testEvent("one"), testToken(t, "one"))
	require.NoError(t, err)
	b.opts.BatchBytes = request.credit*2 - 1
	b.mu.Unlock()
	for _, id := range []string{"one", "two", "six"} {
		require.NoError(t, b.Enqueue(context.Background(), testEvent(id), testToken(t, id)))
	}
	closeBuffer(t, b)
	close(callbacks)
	var count int
	for batch := range callbacks {
		require.Len(t, batch.Records, 1)
		count++
	}
	require.Equal(t, 3, count)
}

func TestCoalescerUsesInputOrderAndPreservesProvenance(t *testing.T) {
	first, second, update := testEvent("z"), testEvent("a"), testEvent("z")
	first.EventID, second.EventID, update.EventID = "z", "a", "0"
	update.OpType = events.StoreOperationUpdate
	update.Backend = "mongo-a"
	result := CoalesceEvents([]*events.StoreChangeEvent{first, second, update})
	require.Len(t, result, 2)
	require.Equal(t, "a", result[0].MgoDocID)
	require.Equal(t, "z", result[1].MgoDocID)
	require.Equal(t, "mongo-a", result[1].Backend)
	require.Equal(t, events.StoreOperationInsert, result[1].OpType)
	require.Equal(t, events.StoreOperationUpdate, update.OpType)
}

func TestFlushReconnectBoundaryPrecedesIdentityRetention(t *testing.T) {
	opts := testOptions(t.TempDir())
	opts.BatchSize = 100
	opts.BatchInterval = time.Hour
	callbacks := make(chan CommittedBatch, 10)
	opts.OnCommit = func(batch CommittedBatch) { callbacks <- batch }
	b, err := New(opts)
	require.NoError(t, err)
	defer closeBuffer(t, b)
	startAt := &primitive.Timestamp{T: 100}
	require.NoError(t, b.InitializeBoundary(context.Background(), nil, startAt))
	committed(t, callbacks)
	for _, id := range []string{"original-a", "original-b"} {
		require.NoError(t, b.Enqueue(context.Background(), testEvent(id), testToken(t, id)))
	}
	stale, err := b.State()
	require.NoError(t, err)
	require.Zero(t, stale.Position.Sequence)
	require.Empty(t, stale.ResumeToken)

	// Reconnection obtains a barrier result, even though the incomplete batch is
	// configured to wait an hour. Both originals precede the selected token.
	reconnect, err := b.Flush(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(2), reconnect.Position.Sequence)
	require.Equal(t, testToken(t, "original-b"), reconnect.ResumeToken)
	published := committed(t, callbacks)
	require.Len(t, published.Records, 2)
	require.Equal(t, reconnect, published.State)

	require.NoError(t, b.Retain(context.Background(), time.Time{}, 1))
	_, closer, err := b.db.Get(identityKey(reconnect.Position.Generation, eventIdentity(opts.SourceID, testToken(t, "original-a"))))
	require.ErrorIs(t, err, pebble.ErrNotFound)
	require.Nil(t, closer)

	source := []string{"original-a", "original-b", "new-c"}
	resumed := false
	for _, id := range source {
		if !resumed {
			if string(testToken(t, id)) == string(reconnect.ResumeToken) {
				resumed = true
			}
			continue
		}
		require.NoError(t, b.Enqueue(context.Background(), testEvent(id), testToken(t, id)))
	}
	require.True(t, resumed)
	state, err := b.Flush(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(3), state.Position.Sequence)
	require.Equal(t, testToken(t, "new-c"), state.ResumeToken)
	page, err := b.ReadPage(context.Background(), reconnect.Position, state.Position, 10, 1<<16)
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	require.Equal(t, "new-c", page.Records[0].Event.MgoDocID)
}

func TestInitialSourceBoundarySurvivesRestartAndCannotOverwriteProgress(t *testing.T) {
	for _, boundary := range []string{"operation time", "resume token"} {
		t.Run(boundary, func(t *testing.T) {
			opts := testOptions(t.TempDir())
			b, err := New(opts)
			require.NoError(t, err)
			var token bson.Raw
			var startAt *primitive.Timestamp
			if boundary == "operation time" {
				startAt = &primitive.Timestamp{T: 123, I: 4}
			} else {
				token = testToken(t, "initial-boundary")
			}
			require.NoError(t, b.InitializeBoundary(context.Background(), token, startAt))
			expected, err := b.State()
			require.NoError(t, err)
			require.Zero(t, expected.Position.Sequence)
			if startAt != nil {
				startAt.T++
			} else {
				token[5] ^= 1
			}
			closeBuffer(t, b)
			b, err = New(opts)
			require.NoError(t, err)
			state, err := b.Flush(context.Background())
			require.NoError(t, err)
			require.Equal(t, expected, state)
			require.NoError(t, b.InitializeBoundary(context.Background(), testToken(t, "different-boundary"), nil))
			unchanged, err := b.State()
			require.NoError(t, err)
			require.Equal(t, expected, unchanged)

			require.NoError(t, b.Enqueue(context.Background(), testEvent("first"), testToken(t, "first")))
			state, err = b.Flush(context.Background())
			require.NoError(t, err)
			require.Equal(t, uint64(1), state.Position.Sequence)
			require.Nil(t, state.StartAt)
			require.Equal(t, testToken(t, "first"), state.ResumeToken)
			require.NoError(t, b.InitializeBoundary(context.Background(), nil, &primitive.Timestamp{T: 456}))
			unchanged, err = b.State()
			require.NoError(t, err)
			require.Equal(t, state, unchanged)
			closeBuffer(t, b)
		})
	}
}

func TestInitialBoundaryWaitsForSyncAndDoesNotPublishReadinessEarly(t *testing.T) {
	opts := testOptions(t.TempDir())
	visible, release := make(chan struct{}), make(chan struct{})
	opts.newBatch = controlledFactory(func(batch pebbleBatch, options *pebble.WriteOptions) error {
		err := batch.Commit(options)
		close(visible)
		<-release
		return err
	})
	b, err := New(opts)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() { result <- b.InitializeBoundary(context.Background(), nil, &primitive.Timestamp{T: 123}) }()
	select {
	case <-visible:
	case <-time.After(5 * time.Second):
		t.Fatal("boundary commit did not start")
	}
	state, err := b.State()
	require.NoError(t, err)
	require.Nil(t, state.StartAt)
	require.Empty(t, state.ResumeToken)
	select {
	case <-result:
		t.Fatal("boundary became ready before Sync completion")
	default:
	}
	close(release)
	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("boundary initialization did not complete")
	}
	state, err = b.State()
	require.NoError(t, err)
	require.Equal(t, &primitive.Timestamp{T: 123}, state.StartAt)
	closeBuffer(t, b)
}

func TestFlushAndBoundaryPropagateCommitFailure(t *testing.T) {
	for _, operation := range []string{"flush", "initialize"} {
		t.Run(operation, func(t *testing.T) {
			opts := testOptions(t.TempDir())
			opts.BatchSize = 100
			opts.BatchInterval = time.Hour
			failure := errors.New("source boundary could not be persisted")
			opts.newBatch = controlledFactory(func(pebbleBatch, *pebble.WriteOptions) error { return failure })
			b, err := New(opts)
			require.NoError(t, err)
			if operation == "flush" {
				require.NoError(t, b.Enqueue(context.Background(), testEvent("pending"), testToken(t, "pending")))
				_, err = b.Flush(context.Background())
			} else {
				err = b.InitializeBoundary(context.Background(), nil, &primitive.Timestamp{T: 123})
			}
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, b.Close(context.Background()), failure)
			reopened, err := New(testOptions(opts.Path))
			require.NoError(t, err)
			state, err := reopened.State()
			require.NoError(t, err)
			require.Zero(t, state.Position.Sequence)
			require.Nil(t, state.StartAt)
			require.Empty(t, state.ResumeToken)
			closeBuffer(t, reopened)
		})
	}
}

func TestCorruptDurableHistoryCannotBeReopenedAsHealthy(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(*testing.T, *pebble.DB, State)
	}{
		{"missing event prefix", func(t *testing.T, db *pebble.DB, state State) {
			require.NoError(t, db.Delete(eventKey(state.Position.Generation, 1), pebble.Sync))
		}},
		{"malformed stored event", func(t *testing.T, db *pebble.DB, state State) {
			require.NoError(t, db.Set(eventKey(state.Position.Generation, 1), []byte("{"), pebble.Sync))
		}},
		{"event position disagrees with key", func(t *testing.T, db *pebble.DB, state State) {
			rewriteDiskEvent(t, db, state, func(record *diskRecord) { record.Sequence = 99 })
		}},
		{"invalid event source token", func(t *testing.T, db *pebble.DB, state State) {
			rewriteDiskEvent(t, db, state, func(record *diskRecord) { record.Token = bson.Raw{1, 2, 3} })
		}},
		{"event payload has wrong shape", func(t *testing.T, db *pebble.DB, state State) {
			rewriteDiskEvent(t, db, state, func(record *diskRecord) { record.Event = json.RawMessage("[]") })
		}},
		{"event identity disagrees with token", func(t *testing.T, db *pebble.DB, state State) {
			rewriteDiskEvent(t, db, state, func(record *diskRecord) {
				var event events.StoreChangeEvent
				require.NoError(t, json.Unmarshal(record.Event, &event))
				event.EventID = "forged-identity"
				value, err := json.Marshal(event)
				require.NoError(t, err)
				record.Event = value
			})
		}},
		{"missing identity", func(t *testing.T, db *pebble.DB, state State) {
			key := identityKey(state.Position.Generation, eventIdentity(state.Position.SourceID, testToken(t, "one")))
			require.NoError(t, db.Delete(key, pebble.Sync))
		}},
		{"malformed identity", func(t *testing.T, db *pebble.DB, state State) {
			key := identityKey(state.Position.Generation, eventIdentity(state.Position.SourceID, testToken(t, "one")))
			require.NoError(t, db.Set(key, []byte("[]"), pebble.Sync))
		}},
		{"identity belongs to another token", func(t *testing.T, db *pebble.DB, state State) {
			key := identityKey(state.Position.Generation, eventIdentity(state.Position.SourceID, testToken(t, "one")))
			value, err := json.Marshal(identityRecord{Sequence: 1, Token: testToken(t, "different")})
			require.NoError(t, err)
			require.NoError(t, db.Set(key, value, pebble.Sync))
		}},
		{"resume token precedes frontier", func(t *testing.T, db *pebble.DB, state State) {
			state.ResumeToken = testToken(t, "one")
			rewriteDiskState(t, db, state)
		}},
		{"event beyond frontier", func(t *testing.T, db *pebble.DB, state State) {
			state.Position.Sequence = 1
			state.ResumeToken = testToken(t, "one")
			rewriteDiskState(t, db, state)
		}},
		{"retained byte accounting differs", func(t *testing.T, db *pebble.DB, state State) {
			state.RetainedBytes++
			rewriteDiskState(t, db, state)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			opts := testOptions(t.TempDir())
			b, err := New(opts)
			require.NoError(t, err)
			for _, id := range []string{"one", "two"} {
				require.NoError(t, b.Enqueue(context.Background(), testEvent(id), testToken(t, id)))
			}
			state, err := b.Flush(context.Background())
			require.NoError(t, err)
			closeBuffer(t, b)
			db, err := pebble.Open(opts.Path, &pebble.Options{})
			require.NoError(t, err)
			test.corrupt(t, db, state)
			require.NoError(t, db.Close())
			reopened, err := New(opts)
			require.Nil(t, reopened)
			assertCode(t, err, events.CodeStorageFailure)
		})
	}
}

func rewriteDiskEvent(t *testing.T, db *pebble.DB, state State, mutate func(*diskRecord)) {
	t.Helper()
	key := eventKey(state.Position.Generation, 1)
	value, closer, err := db.Get(key)
	require.NoError(t, err)
	var record diskRecord
	require.NoError(t, decode(value, &record))
	require.NoError(t, closer.Close())
	mutate(&record)
	value, err = json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, db.Set(key, value, pebble.Sync))
}

func rewriteDiskState(t *testing.T, db *pebble.DB, state State) {
	t.Helper()
	value, err := json.Marshal(diskState{Version: formatVersion, SourceScope: testOptions("").SourceScope, State: state})
	require.NoError(t, err)
	require.NoError(t, db.Set(stateKey, value, pebble.Sync))
}

func TestInvalidMetadataIsRejectedWithoutResettingHistory(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*diskState)
		suffix string
		code   events.ErrorCode
	}{
		{name: "unsupported version", mutate: func(state *diskState) { state.Version++ }, code: events.CodeUnsupportedFormat},
		{name: "trailing object", suffix: " {}", code: events.CodeUnsupportedFormat},
		{name: "trailing invalid JSON", suffix: " x", code: events.CodeUnsupportedFormat},
		{name: "invalid generation", mutate: func(state *diskState) { state.State.Position.Generation = "invalid" }, code: events.CodeStorageFailure},
		{name: "floor beyond frontier", mutate: func(state *diskState) { state.State.DiscardedThrough = 1 }, code: events.CodeStorageFailure},
		{name: "negative byte accounting", mutate: func(state *diskState) { state.State.RetainedBytes = -1 }, code: events.CodeStorageFailure},
		{name: "invalid resume token", mutate: func(state *diskState) { state.State.ResumeToken = bson.Raw{1, 2, 3} }, code: events.CodeStorageFailure},
		{name: "two conflicting boundaries", mutate: func(state *diskState) {
			state.State.StartAt = &primitive.Timestamp{T: 1}
			state.State.ResumeToken = testToken(t, "initial")
		}, code: events.CodeStorageFailure},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			opts := testOptions(t.TempDir())
			b, err := New(opts)
			require.NoError(t, err)
			state, err := b.State()
			require.NoError(t, err)
			closeBuffer(t, b)
			metadata := diskState{Version: formatVersion, SourceScope: opts.SourceScope, State: state}
			if test.mutate != nil {
				test.mutate(&metadata)
			}
			encoded, err := json.Marshal(metadata)
			require.NoError(t, err)
			encoded = append(encoded, test.suffix...)
			db, err := pebble.Open(opts.Path, &pebble.Options{})
			require.NoError(t, err)
			require.NoError(t, db.Set(stateKey, encoded, pebble.Sync))
			require.NoError(t, db.Close())
			reopened, err := New(opts)
			require.Nil(t, reopened)
			assertCode(t, err, test.code)
			db, err = pebble.Open(opts.Path, &pebble.Options{})
			require.NoError(t, err)
			preserved, closer, err := db.Get(stateKey)
			require.NoError(t, err)
			require.Equal(t, encoded, preserved)
			require.NoError(t, closer.Close())
			require.NoError(t, db.Close())
		})
	}
}

func TestClosedAndFailedBuffersCannotAcceptOrExposeWork(t *testing.T) {
	opts := testOptions(t.TempDir())
	b, err := New(opts)
	require.NoError(t, err)
	state, err := b.State()
	require.NoError(t, err)
	closeBuffer(t, b)
	_, err = b.State()
	require.ErrorContains(t, err, "closed")
	_, err = b.ReadPage(context.Background(), state.Position, state.Position, 1, 1000)
	require.ErrorContains(t, err, "closed")
	_, err = b.Flush(context.Background())
	require.ErrorContains(t, err, "closing")
	require.ErrorContains(t, b.Enqueue(context.Background(), testEvent("late"), testToken(t, "late")), "closing")

	failure := errors.New("injected storage failure")
	opts = testOptions(t.TempDir())
	opts.newBatch = controlledFactory(func(pebbleBatch, *pebble.WriteOptions) error { return failure })
	b, err = New(opts)
	require.NoError(t, err)
	require.NoError(t, b.Enqueue(context.Background(), testEvent("failing"), testToken(t, "failing")))
	require.ErrorIs(t, b.Close(context.Background()), failure)
	_, err = b.State()
	require.ErrorIs(t, err, failure)
	_, err = b.ReadPage(context.Background(), state.Position, state.Position, 1, 1000)
	require.ErrorIs(t, err, failure)
	_, err = b.Flush(context.Background())
	require.ErrorIs(t, err, failure)
}

func TestBoundaryAndAdmissionRejectInvalidInputWithoutChangingState(t *testing.T) {
	opts := testOptions(t.TempDir())
	b, err := New(opts)
	require.NoError(t, err)
	defer closeBuffer(t, b)
	before, err := b.State()
	require.NoError(t, err)
	require.Error(t, b.InitializeBoundary(context.Background(), nil, nil))
	require.Error(t, b.InitializeBoundary(context.Background(), testToken(t, "both"), &primitive.Timestamp{T: 1}))
	require.Error(t, b.InitializeBoundary(context.Background(), bson.Raw{1, 2, 3}, nil))
	require.Error(t, b.InitializeBoundary(context.Background(), nil, &primitive.Timestamp{}))
	require.Error(t, b.Enqueue(context.Background(), nil, testToken(t, "nil")))
	require.Error(t, b.Enqueue(context.Background(), testEvent("no-token"), nil))
	require.Error(t, b.Enqueue(context.Background(), testEvent("bad-token"), bson.Raw{1, 2, 3}))
	invalid := testEvent("unsupported-data")
	invalid.FullDocument.Data["unsupported"] = make(chan int)
	require.Error(t, b.Enqueue(context.Background(), invalid, testToken(t, "unsupported-data")))
	require.Error(t, b.MarkDiscontinuous(context.Background(), nil))
	require.Error(t, b.Retain(context.Background(), time.Time{}, -1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = b.Flush(ctx)
	require.ErrorIs(t, err, context.Canceled)
	after, err := b.State()
	require.NoError(t, err)
	require.Equal(t, before, after)
}
