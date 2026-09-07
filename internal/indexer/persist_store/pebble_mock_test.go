package persist_store

import (
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/stretchr/testify/require"
)

// mockDB is a mock implementation of the DB interface for testing error paths.
type mockDB struct {
	mu      sync.Mutex
	data    map[string][]byte
	batches []*mockBatch

	// Error injection
	getErr        error
	setErr        error
	deleteErr     error
	newIterErr    error
	closeErr      error
	iterError     error // Error returned by Iterator.Error()
	batchSetErr   error // Error for batch.Set
	batchDelErr   error // Error for batch.Delete
	commitErr     error // Error for batch.Commit
	commitStarted chan struct{}
	commitGate    chan struct{}
}

func newMockDB() *mockDB {
	return &mockDB{
		data: make(map[string][]byte),
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func (m *mockDB) Get(key []byte) (value []byte, closer io.Closer, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.getErr != nil {
		return nil, nil, m.getErr
	}

	v, ok := m.data[string(key)]
	if !ok {
		return nil, nil, pebble.ErrNotFound
	}
	// Return a copy to avoid data races
	cp := make([]byte, len(v))
	copy(cp, v)
	return cp, nopCloser{}, nil
}

func (m *mockDB) Set(key, value []byte, o *pebble.WriteOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.setErr != nil {
		return m.setErr
	}

	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[string(key)] = cp
	return nil
}

func (m *mockDB) Delete(key []byte, o *pebble.WriteOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	delete(m.data, string(key))
	return nil
}

func (m *mockDB) NewIter(o *pebble.IterOptions) (Iterator, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.newIterErr != nil {
		return nil, m.newIterErr
	}

	// Collect keys within bounds
	var keys []string
	for k := range m.data {
		kb := []byte(k)
		if o != nil {
			if o.LowerBound != nil && string(kb) < string(o.LowerBound) {
				continue
			}
			if o.UpperBound != nil && string(kb) >= string(o.UpperBound) {
				continue
			}
		}
		keys = append(keys, k)
	}

	return &mockIterator{
		keys:    keys,
		data:    m.data,
		pos:     -1,
		iterErr: m.iterError,
	}, nil
}

func (m *mockDB) NewBatch() Batch {
	m.mu.Lock()
	defer m.mu.Unlock()

	b := &mockBatch{
		parent:        m,
		ops:           make([]batchOp, 0),
		setErr:        m.batchSetErr,
		deleteErr:     m.batchDelErr,
		commitErr:     m.commitErr,
		commitStarted: m.commitStarted,
		commitGate:    m.commitGate,
	}
	m.commitStarted = nil
	m.commitGate = nil
	m.batches = append(m.batches, b)
	return b
}

func (m *mockDB) Close() error {
	if m.closeErr != nil {
		return m.closeErr
	}
	return nil
}

// mockIterator implements Iterator for testing.
type mockIterator struct {
	keys    []string
	data    map[string][]byte
	pos     int
	iterErr error
}

func (m *mockIterator) First() bool {
	if len(m.keys) == 0 {
		return false
	}
	m.pos = 0
	return true
}

func (m *mockIterator) Valid() bool {
	return m.pos >= 0 && m.pos < len(m.keys)
}

func (m *mockIterator) Key() []byte {
	if !m.Valid() {
		return nil
	}
	return []byte(m.keys[m.pos])
}

func (m *mockIterator) Value() []byte {
	if !m.Valid() {
		return nil
	}
	return m.data[m.keys[m.pos]]
}

func (m *mockIterator) Next() bool {
	m.pos++
	return m.Valid()
}

func (m *mockIterator) Error() error {
	return m.iterErr
}

func (m *mockIterator) Close() error {
	return nil
}

// batchOp represents a single batch operation.
type batchOp struct {
	isSet bool
	key   []byte
	value []byte
}

// mockBatch implements Batch for testing.
type mockBatch struct {
	parent        *mockDB
	ops           []batchOp
	setErr        error
	deleteErr     error
	commitErr     error
	commitStarted chan struct{}
	commitGate    chan struct{}
	closed        bool
}

func (b *mockBatch) Set(key, value []byte, opt *pebble.WriteOptions) error {
	if b.setErr != nil {
		return b.setErr
	}
	b.ops = append(b.ops, batchOp{isSet: true, key: append([]byte(nil), key...), value: append([]byte(nil), value...)})
	return nil
}

func (b *mockBatch) Delete(key []byte, opt *pebble.WriteOptions) error {
	if b.deleteErr != nil {
		return b.deleteErr
	}
	b.ops = append(b.ops, batchOp{isSet: false, key: append([]byte(nil), key...)})
	return nil
}

func (b *mockBatch) Commit(o *pebble.WriteOptions) error {
	if b.commitStarted != nil {
		close(b.commitStarted)
		<-b.commitGate
	}
	if b.commitErr != nil {
		return b.commitErr
	}

	b.parent.mu.Lock()
	defer b.parent.mu.Unlock()

	for _, op := range b.ops {
		if op.isSet {
			b.parent.data[string(op.key)] = op.value
		} else {
			delete(b.parent.data, string(op.key))
		}
	}
	return nil
}

func (b *mockBatch) Close() error {
	b.closed = true
	return nil
}

// newMockPebbleStore creates a PebbleStore with a mock DB for testing.
func newMockPebbleStore(db *mockDB) *PebbleStore {
	return &PebbleStore{
		db:                  db,
		path:                "/mock/path",
		logger:              slog.Default(),
		pending:             make(map[string]map[string]*pendingOp),
		flushing:            make(map[string]map[string]*pendingOp),
		pendingIndexDeletes: make(map[string]indexDeleteOp),
		notifyCh:            make(chan struct{}, 1),
		closeCh:             make(chan struct{}),
		failed:              make(chan struct{}),
		flushDoneCh:         make(chan struct{}, 1),
		batchSize:           100,
		batchInterval:       50 * time.Millisecond,
	}
}

// ============ Error Path Tests ============

// TestApplyOpDeleteReadError tests applyOp when db.Get returns an error during delete.
func TestApplyOpDeleteReadError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Create a delete operation (orderKey is nil)
	op := &pendingOp{
		db:      "testdb",
		pattern: "users/*",
		tmplID:  "tmpl1",
		hash:    "hash1",
		docID:   "doc1",
		revKey:  reverseKey("testdb", "users/*", "tmpl1", "doc1"),
		idxKey:  nil, // delete operation
	}

	// Inject error for Get
	db.getErr = errors.New("mock read error")

	batch := db.NewBatch()
	err := ps.applyOp(batch, op)

	if err == nil {
		t.Error("expected error from applyOp, got nil")
	}
	if err != nil && !errors.Is(err, db.getErr) {
		// Check error message contains our error
		if err.Error() != "failed to read reverse index: mock read error" {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

// TestApplyOpDeleteBatchDeleteError tests applyOp when batch.Delete fails during delete.
func TestApplyOpDeleteBatchDeleteError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// First, set up data so Get succeeds
	revKey := reverseKey("testdb", "users/*", "tmpl1", "doc1")
	db.data[string(revKey)] = []byte{0x01, 0x02}

	// Create a delete operation
	op := &pendingOp{
		db:       "testdb",
		pattern:  "users/*",
		tmplID:   "tmpl1",
		hash:     "hash1",
		docID:    "doc1",
		revKey:   revKey,
		orderKey: nil, // delete operation
	}

	// Inject error for batch.Delete
	db.batchDelErr = errors.New("mock batch delete error")

	batch := db.NewBatch()
	err := ps.applyOp(batch, op)

	if err == nil {
		t.Error("expected error from applyOp, got nil")
	}
}

// TestApplyOpUpsertReadError tests applyOp when db.Get returns an error during upsert.
func TestApplyOpUpsertReadError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	orderKey := []byte{0x01, 0x02}
	op := &pendingOp{
		db:       "testdb",
		pattern:  "users/*",
		tmplID:   "tmpl1",
		hash:     "hash1",
		docID:    "doc1",
		orderKey: orderKey,
		revKey:   reverseKey("testdb", "users/*", "tmpl1", "doc1"),
		idxKey:   indexKey("testdb", "users/*", "tmpl1", orderKey),
		mapKey:   mapKey("testdb", "users/*", "tmpl1"),
	}

	// Inject error for Get (not ErrNotFound)
	db.getErr = errors.New("mock upsert read error")

	batch := db.NewBatch()
	err := ps.applyOp(batch, op)

	if err == nil {
		t.Error("expected error from applyOp, got nil")
	}
}

// TestApplyOpUpsertDeleteOldError tests applyOp when batch.Delete fails for old index entry.
func TestApplyOpUpsertDeleteOldError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Set up existing data
	revKey := reverseKey("testdb", "users/*", "tmpl1", "doc1")
	oldOrderKey := []byte{0x00, 0x01}
	db.data[string(revKey)] = oldOrderKey

	orderKey := []byte{0x01, 0x02}
	op := &pendingOp{
		db:       "testdb",
		pattern:  "users/*",
		tmplID:   "tmpl1",
		hash:     "hash1",
		docID:    "doc1",
		orderKey: orderKey,
		revKey:   revKey,
		idxKey:   indexKey("testdb", "users/*", "tmpl1", orderKey),
		mapKey:   mapKey("testdb", "users/*", "tmpl1"),
	}

	// Inject error for batch.Delete
	db.batchDelErr = errors.New("mock batch delete old error")

	batch := db.NewBatch()
	err := ps.applyOp(batch, op)

	if err == nil {
		t.Error("expected error from applyOp, got nil")
	}
}

// TestApplyOpUpsertSetError tests applyOp when batch.Set fails.
func TestApplyOpUpsertSetError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	orderKey := []byte{0x01, 0x02}
	op := &pendingOp{
		db:       "testdb",
		pattern:  "users/*",
		tmplID:   "tmpl1",
		hash:     "hash1",
		docID:    "doc1",
		orderKey: orderKey,
		revKey:   reverseKey("testdb", "users/*", "tmpl1", "doc1"),
		idxKey:   indexKey("testdb", "users/*", "tmpl1", orderKey),
		mapKey:   mapKey("testdb", "users/*", "tmpl1"),
	}

	// Inject error for batch.Set
	db.batchSetErr = errors.New("mock batch set error")

	batch := db.NewBatch()
	err := ps.applyOp(batch, op)

	if err == nil {
		t.Error("expected error from applyOp, got nil")
	}
}

// TestDeleteByPrefixIterError tests deleteByPrefix when NewIter fails.
func TestDeleteByPrefixIterError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Inject error for NewIter
	db.newIterErr = errors.New("mock iter error")

	err := ps.deleteByPrefix([]byte("prefix"))

	if err == nil {
		t.Error("expected error from deleteByPrefix, got nil")
	}
}

// TestDeleteByPrefixBatchDeleteError tests deleteByPrefix when batch.Delete fails.
func TestDeleteByPrefixBatchDeleteError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Add some data with the prefix
	db.data["prefix:key1"] = []byte("value1")
	db.data["prefix:key2"] = []byte("value2")

	// Inject error for batch.Delete
	db.batchDelErr = errors.New("mock batch delete error")

	err := ps.deleteByPrefix([]byte("prefix"))

	if err == nil {
		t.Error("expected error from deleteByPrefix, got nil")
	}
}

// TestDeleteByPrefixIteratorError tests deleteByPrefix when iterator has error.
func TestDeleteByPrefixIteratorError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Add some data with the prefix
	db.data["prefix:key1"] = []byte("value1")

	// Inject error for iterator.Error()
	db.iterError = errors.New("mock iterator error")

	err := ps.deleteByPrefix([]byte("prefix"))

	if err == nil {
		t.Error("expected error from deleteByPrefix, got nil")
	}
}

// TestDeleteByPrefixCommitError tests deleteByPrefix when batch.Commit fails.
func TestDeleteByPrefixCommitError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Add some data with the prefix
	db.data["prefix:key1"] = []byte("value1")

	// Inject error for batch.Commit
	db.commitErr = errors.New("mock commit error")

	err := ps.deleteByPrefix([]byte("prefix"))

	if err == nil {
		t.Error("expected error from deleteByPrefix, got nil")
	}
}

// TestExecuteIndexDeleteFirstPrefixError tests executeIndexDelete when first deleteByPrefix fails.
func TestExecuteIndexDeleteFirstPrefixError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	delOp := indexDeleteOp{
		db:      "testdb",
		pattern: "users/*",
		tmplID:  "tmpl1",
		hash:    "hash1",
	}

	// Inject error for NewIter (will fail first deleteByPrefix)
	db.newIterErr = errors.New("mock iter error")

	err := ps.executeIndexDelete(delOp)

	if err == nil {
		t.Error("expected error from executeIndexDelete, got nil")
	}
}

// TestExecuteIndexDeleteMapDeleteError tests executeIndexDelete when map entry delete fails.
func TestExecuteIndexDeleteMapDeleteError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	delOp := indexDeleteOp{
		db:      "testdb",
		pattern: "users/*",
		tmplID:  "tmpl1",
		hash:    "hash1",
	}

	// Inject error for direct Delete (map/state entries)
	db.deleteErr = errors.New("mock delete error")

	err := ps.executeIndexDelete(delOp)

	if err == nil {
		t.Error("expected error from executeIndexDelete, got nil")
	}
}

// TestExecuteIndexDeleteSecondPrefixError tests executeIndexDelete when second deleteByPrefix (rev entries) fails.
func TestExecuteIndexDeleteSecondPrefixError(t *testing.T) {
	db := &mockDBWithCallCounter{mockDB: newMockDB()}
	ps := newMockPebbleStore(db.mockDB)
	ps.db = db // Replace with call counter wrapper

	delOp := indexDeleteOp{
		db:      "testdb",
		pattern: "users/*",
		tmplID:  "tmpl1",
		hash:    "hash1",
	}

	// Fail on second NewIter call (for rev prefix)
	db.failNewIterOnCall = 2

	err := ps.executeIndexDelete(delOp)

	if err == nil {
		t.Error("expected error from executeIndexDelete, got nil")
	}
	// Should fail on "failed to delete reverse entries"
	if err != nil && !contains(err.Error(), "reverse") {
		t.Logf("error message: %v", err)
	}
}

// TestExecuteIndexDeleteStateDeleteError tests executeIndexDelete when state entry delete fails.
func TestExecuteIndexDeleteStateDeleteError(t *testing.T) {
	db := &mockDBWithCallCounter{mockDB: newMockDB()}
	ps := newMockPebbleStore(db.mockDB)
	ps.db = db // Replace with call counter wrapper

	delOp := indexDeleteOp{
		db:      "testdb",
		pattern: "users/*",
		tmplID:  "tmpl1",
		hash:    "hash1",
	}

	// Fail on second Delete call (for state key, after map key)
	db.failDeleteOnCall = 2

	err := ps.executeIndexDelete(delOp)

	if err == nil {
		t.Error("expected error from executeIndexDelete, got nil")
	}
	// Should fail on "failed to delete state entry"
	if err != nil && !contains(err.Error(), "state") {
		t.Logf("error message: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockDBWithCallCounter wraps mockDB to fail on specific call counts.
type mockDBWithCallCounter struct {
	*mockDB
	newIterCallCount  int
	failNewIterOnCall int
	deleteCallCount   int
	failDeleteOnCall  int
}

func (m *mockDBWithCallCounter) NewIter(o *pebble.IterOptions) (Iterator, error) {
	m.newIterCallCount++
	if m.failNewIterOnCall > 0 && m.newIterCallCount == m.failNewIterOnCall {
		return nil, errors.New("mock iter error on call")
	}
	return m.mockDB.NewIter(o)
}

func (m *mockDBWithCallCounter) Delete(key []byte, o *pebble.WriteOptions) error {
	m.deleteCallCount++
	if m.failDeleteOnCall > 0 && m.deleteCallCount == m.failDeleteOnCall {
		return errors.New("mock delete error on call")
	}
	return m.mockDB.Delete(key, o)
}

func (m *mockDBWithCallCounter) Get(key []byte) (value []byte, closer io.Closer, err error) {
	return m.mockDB.Get(key)
}

func (m *mockDBWithCallCounter) Set(key, value []byte, o *pebble.WriteOptions) error {
	return m.mockDB.Set(key, value, o)
}

func (m *mockDBWithCallCounter) NewBatch() Batch {
	return m.mockDB.NewBatch()
}

func (m *mockDBWithCallCounter) Close() error {
	return m.mockDB.Close()
}

// ============ DeleteDatabase Error Path Tests ============

// TestDeleteDatabaseListIndexesError tests DeleteDatabase when ListIndexes returns an error.
func TestDeleteDatabaseListIndexesError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Inject error for NewIter to make ListIndexes fail
	db.newIterErr = errors.New("mock list indexes iterator error")

	err := ps.DeleteDatabase("testdb")

	if err == nil {
		t.Error("expected error from DeleteDatabase, got nil")
	}
	if err != nil && !contains(err.Error(), "failed to list indexes") {
		t.Errorf("expected error to contain 'failed to list indexes', got: %v", err)
	}
}

// TestDeleteDatabaseDeleteIndexError tests DeleteDatabase when DeleteIndex fails but continues.
func TestDeleteDatabaseDeleteIndexError(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// First, add some index entries so ListIndexes returns results
	// Add a map entry: key = "m|{db}|{hash}", value = "{pattern}|{tmplID}"
	pattern := "users/*"
	tmplID := "tmpl1"
	mapK := mapKey("testdb", pattern, tmplID)
	db.data[string(mapK)] = []byte(pattern + "|" + tmplID)

	// Also add an index entry so DeleteIndex has something to delete
	orderKey := []byte{0x01, 0x02}
	idxK := indexKey("testdb", pattern, tmplID, orderKey)
	db.data[string(idxK)] = []byte("doc1")

	// Add reverse entry
	revK := reverseKey("testdb", pattern, tmplID, "doc1")
	db.data[string(revK)] = orderKey

	// Add state entry
	stateK := stateKey("testdb", pattern, tmplID)
	db.data[string(stateK)] = []byte("healthy")

	// Now make DeleteIndex fail by injecting error on first NewIter after listing
	// Use mockDBWithCallCounter to fail on second NewIter call (first is for ListIndexes)
	dbWithCounter := &mockDBWithCallCounter{
		mockDB:            db,
		failNewIterOnCall: 2, // Fail on second call (DeleteIndex's deleteByPrefix)
	}
	ps.db = dbWithCounter

	// DeleteDatabase should not return error even if DeleteIndex fails
	err := ps.DeleteDatabase("testdb")

	// Should not return error because DeleteIndex errors are logged but not propagated
	if err != nil {
		t.Errorf("expected no error from DeleteDatabase (errors should be logged), got: %v", err)
	}
}

// TestDeleteDatabaseEmptyDatabase tests DeleteDatabase on a database with no indexes.
func TestDeleteDatabaseEmptyDatabase(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Delete database with no indexes - should succeed with no error
	err := ps.DeleteDatabase("nonexistent")

	if err != nil {
		t.Errorf("expected no error for empty database, got: %v", err)
	}
}

// TestDeleteDatabaseMultipleIndexes tests DeleteDatabase with multiple indexes.
func TestDeleteDatabaseMultipleIndexes(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Add multiple index entries
	indexes := []struct {
		pattern string
		tmplID  string
	}{
		{"users/*", "tmpl1"},
		{"posts/*", "tmpl2"},
		{"comments/*", "tmpl3"},
	}

	for _, idx := range indexes {
		mapK := mapKey("testdb", idx.pattern, idx.tmplID)
		db.data[string(mapK)] = []byte(idx.pattern + "|" + idx.tmplID)

		// Add state entry
		stateK := stateKey("testdb", idx.pattern, idx.tmplID)
		db.data[string(stateK)] = []byte("healthy")
	}

	// DeleteDatabase should succeed
	err := ps.DeleteDatabase("testdb")

	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// TestDeleteDatabaseWithDeleteIndexMultipleErrors tests that DeleteDatabase continues
// even when multiple DeleteIndex calls fail.
func TestDeleteDatabaseWithDeleteIndexMultipleErrors(t *testing.T) {
	db := newMockDB()
	ps := newMockPebbleStore(db)

	// Add multiple index entries
	indexes := []struct {
		pattern string
		tmplID  string
	}{
		{"users/*", "tmpl1"},
		{"posts/*", "tmpl2"},
	}

	for _, idx := range indexes {
		mapK := mapKey("testdb", idx.pattern, idx.tmplID)
		db.data[string(mapK)] = []byte(idx.pattern + "|" + idx.tmplID)
	}

	// Inject delete error to make DeleteIndex fail
	db.deleteErr = errors.New("mock delete error")

	// DeleteDatabase should not return error, just log the failures
	err := ps.DeleteDatabase("testdb")

	if err != nil {
		t.Errorf("expected no error (errors should be logged), got: %v", err)
	}
}

func TestBatchFailureFencesLaterCheckpoint(t *testing.T) {
	for _, failure := range []string{"operation", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db := newMockDB()
			s := newMockPebbleStore(db)
			require.NoError(t, s.Upsert("db", "docs", "index", "first", []byte{1}, "p1"))
			s.doFlush()
			require.NoError(t, s.Err())
			cause := errors.New("injected disk failure")
			if failure == "operation" {
				db.batchSetErr = cause
			} else {
				db.commitErr = cause
			}
			require.NoError(t, s.Upsert("db", "docs", "index", "second", []byte{2}, ""))
			require.NoError(t, s.SaveProgress("p2"))
			s.doFlush()
			require.ErrorIs(t, s.Err(), cause)
			require.ErrorIs(t, s.Flush(), cause)
			select {
			case <-s.Failed():
			default:
				t.Fatal("write failure did not wake consumers")
			}
			require.ErrorIs(t, s.Upsert("db", "docs", "index", "third", []byte{3}, "p3"), cause)
			require.ErrorIs(t, s.Delete("db", "docs", "index", "first", "p3"), cause)
			require.ErrorIs(t, s.SaveProgress("p3"), cause)
			before := len(db.batches)
			s.doFlush()
			require.Len(t, db.batches, before)
			require.Equal(t, "p1", string(db.data[keyProgress]))
			require.ErrorIs(t, s.Close(), cause)
		})
	}
}

func TestProgressOnlyBatchPersists(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	require.NoError(t, s.SaveProgress("completed-empty-window"))
	require.NoError(t, s.Flush())
	value, closer, err := s.db.Get([]byte(keyProgress))
	require.NoError(t, err)
	defer closer.Close()
	require.Equal(t, "completed-empty-window", string(value))
}

func TestFlushWaitsForProgressOnlySync(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			db := newMockDB()
			started, release := make(chan struct{}), make(chan struct{})
			db.commitStarted, db.commitGate = started, release
			cause := errors.New("sync failed")
			if fail {
				db.commitErr = cause
			}
			s := newMockPebbleStore(db)
			require.NoError(t, s.SaveProgress("p1"))
			commitDone := make(chan struct{})
			go func() { s.doFlush(); close(commitDone) }()
			<-started
			flushDone := make(chan error, 1)
			go func() { flushDone <- s.Flush() }()
			select {
			case err := <-flushDone:
				close(release)
				<-commitDone
				t.Fatalf("Flush returned before Sync completed: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			<-commitDone
			select {
			case err := <-flushDone:
				if fail {
					require.ErrorIs(t, err, cause)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(time.Second):
				t.Fatal("Flush did not observe Sync completion")
			}
		})
	}
}

func TestIndexRemovalStaysHiddenUntilCommitCompletes(t *testing.T) {
	db := newMockDB()
	s := newMockPebbleStore(db)
	require.NoError(t, s.Upsert("db", "docs", "index", "first", []byte{1}, "p1"))
	s.doFlush()
	started, release := make(chan struct{}), make(chan struct{})
	db.commitStarted, db.commitGate = started, release
	require.NoError(t, s.DeleteIndex("db", "docs", "index"))
	done := make(chan struct{})
	go func() { s.doFlush(); close(done) }()
	<-started
	indexes, err := s.ListIndexes("db")
	require.NoError(t, err)
	require.Empty(t, indexes)
	_, found := s.Get("db", "docs", "index", "first")
	require.False(t, found)
	close(release)
	<-done
	require.NoError(t, s.Flush())
	indexes, err = s.ListIndexes("db")
	require.NoError(t, err)
	require.Empty(t, indexes)
}
