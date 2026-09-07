package persist_store

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/bloom"
	"github.com/syntrixbase/syntrix/internal/indexer/config"
	"github.com/syntrixbase/syntrix/internal/indexer/store"
)

// PebbleStore implements Store using PebbleDB.
type PebbleStore struct {
	db     DB
	path   string
	logger *slog.Logger

	// Async batching
	mu                   sync.RWMutex
	pending              map[string]map[string]*pendingOp // outer: "{db}|{hash}", inner: "{docID}"
	flushing             map[string]map[string]*pendingOp
	flushInProgress      bool
	pendingProgress      string // progress to save with next batch
	pendingIndexDeletes  map[string]indexDeleteOp
	flushingIndexDeletes map[string]indexDeleteOp // key: "{db}|{hash}"
	notifyCh             chan struct{}
	closeCh              chan struct{}
	closed               bool
	terminalErr          error
	failed               chan struct{}
	flushDoneCh          chan struct{} // buffered(1), signaled after each flush

	batchSize     int
	batchInterval time.Duration
	batcherWG     sync.WaitGroup
}

// indexDeleteOp represents a pending index delete operation.
type indexDeleteOp struct {
	db      string
	pattern string
	tmplID  string
	hash    string
}

// pendingOp represents a pending write operation.
type pendingOp struct {
	db       string
	pattern  string
	tmplID   string
	hash     string // hex(xxHash64(pattern + "|" + tmplID))
	docID    string
	orderKey []byte // nil = delete

	// Precomputed keys for batch apply
	idxKey []byte // idx/{db}/{hash}/{orderKey}
	revKey []byte // rev/{db}/{hash}/{docID}
	mapKey []byte // map/{db}/{hash}
}

// NewPebbleStore creates a new PebbleStore.
func NewPebbleStore(cfg config.StoreConfig, logger *slog.Logger) (*PebbleStore, error) {
	if cfg.Path == "" {
		return nil, fmt.Errorf("store path is required")
	}
	logger = logger.With("component", "index-store")

	// Ensure directory exists
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %w", err)
	}

	// Configure PebbleDB
	cache := pebble.NewCache(cfg.BlockCacheSize)
	defer cache.Unref()

	dbOpts := &pebble.Options{
		Cache: cache,
		Levels: []pebble.LevelOptions{
			{FilterPolicy: bloom.FilterPolicy(10)}, // 10 bits per key, ~1% false positive
		},
	}

	db, err := pebble.Open(cfg.Path, dbOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to open pebble database: %w", err)
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	batchInterval := cfg.BatchInterval
	if batchInterval <= 0 {
		batchInterval = 100 * time.Millisecond
	}

	s := &PebbleStore{
		db:                  &PebbleDB{db: db},
		path:                cfg.Path,
		logger:              logger,
		pending:             make(map[string]map[string]*pendingOp),
		flushing:            make(map[string]map[string]*pendingOp),
		pendingIndexDeletes: make(map[string]indexDeleteOp),
		notifyCh:            make(chan struct{}, 1),
		closeCh:             make(chan struct{}),
		failed:              make(chan struct{}),
		flushDoneCh:         make(chan struct{}, 1),
		batchSize:           batchSize,
		batchInterval:       batchInterval,
	}

	s.startBatcher()

	return s, nil
}

// Close closes the store.
func (s *PebbleStore) Close() error {
	s.mu.Lock()
	if s.closed {
		err := s.terminalErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	s.mu.Unlock()

	// Signal batcher to stop
	close(s.closeCh)

	// Wait for batcher to finish
	s.batcherWG.Wait()

	// Close PebbleDB
	return errors.Join(s.Err(), s.db.Close())
}

// Flush forces all pending writes to disk and waits for completion.
func (s *PebbleStore) Flush() error {
	// Timeout: max of 5s or 50x batchInterval
	timeout := 5 * time.Second
	if t := s.batchInterval * 50; t > timeout {
		timeout = t
	}
	deadline := time.Now().Add(timeout)

	for {
		// Check if there's anything pending
		s.mu.RLock()
		if s.terminalErr != nil {
			err := s.terminalErr
			s.mu.RUnlock()
			return err
		}
		hasPending := s.flushInProgress || len(s.pending) > 0 || len(s.flushing) > 0 || s.pendingProgress != "" || len(s.pendingIndexDeletes) > 0
		s.mu.RUnlock()

		if !hasPending {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("flush timeout")
		}

		// Trigger flush
		select {
		case s.notifyCh <- struct{}{}:
		default:
		}

		// Wait for batcher to signal completion or timeout
		select {
		case <-s.flushDoneCh:
			// Flush completed, check again
		case <-time.After(100 * time.Millisecond):
			// Fallback timeout in case signal was missed
		}
	}
}

// startBatcher starts the background batcher goroutine.
func (s *PebbleStore) startBatcher() {
	s.batcherWG.Add(1)
	go func() {
		defer s.batcherWG.Done()
		s.batcherLoop()
	}()
}

// batcherLoop is the main loop for the batcher goroutine.
func (s *PebbleStore) batcherLoop() {
	ticker := time.NewTicker(s.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.closeCh:
			// Final flush before exit
			s.doFlush()
			return
		case <-s.notifyCh:
			s.maybeFlush()
		case <-ticker.C:
			s.maybeFlush()
		}
	}
}

// maybeFlush flushes if there are enough pending ops.
// Called from ticker (timeout-based) and notifyCh (triggered).
func (s *PebbleStore) maybeFlush() {
	s.doFlush()
}

// doFlush commits the ordered prefix and its checkpoint together. Any failure
// fences later batches so a checkpoint cannot cross an unapplied operation.
func (s *PebbleStore) doFlush() {
	s.mu.Lock()
	if s.terminalErr != nil || (len(s.pending) == 0 && s.pendingProgress == "" && len(s.pendingIndexDeletes) == 0) {
		s.mu.Unlock()
		return
	}
	s.flushInProgress = true
	s.flushing = s.pending
	s.pending = make(map[string]map[string]*pendingOp)
	progressToSave := s.pendingProgress
	s.pendingProgress = ""
	indexDeletes := s.pendingIndexDeletes
	s.flushingIndexDeletes = indexDeletes
	s.pendingIndexDeletes = make(map[string]indexDeleteOp)
	s.mu.Unlock()

	// Index removals precede the checkpoint-bearing batch. A failed removal
	// leaves the earlier checkpoint available for recovery.
	for _, delOp := range indexDeletes {
		if err := s.executeIndexDelete(delOp); err != nil {
			s.fail(fmt.Errorf("failed to delete index: %w", err))
			return
		}
	}

	batch := s.db.NewBatch()
	defer batch.Close()
	for _, inner := range s.flushing {
		for _, op := range inner {
			if err := s.applyOp(batch, op); err != nil {
				s.fail(fmt.Errorf("failed to apply index operation: %w", err))
				return
			}
		}
	}
	if progressToSave != "" {
		if err := batch.Set([]byte(keyProgress), []byte(progressToSave), nil); err != nil {
			s.fail(fmt.Errorf("failed to set index progress: %w", err))
			return
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		s.fail(fmt.Errorf("failed to commit index batch: %w", err))
		return
	}

	s.mu.Lock()
	s.flushing = make(map[string]map[string]*pendingOp)
	s.flushInProgress = false
	s.flushingIndexDeletes = nil
	s.mu.Unlock()
	select {
	case s.flushDoneCh <- struct{}{}:
	default:
	}
}

func (s *PebbleStore) fail(err error) {
	s.mu.Lock()
	if s.terminalErr == nil {
		s.terminalErr = err
		if s.failed != nil {
			close(s.failed)
		}
	}
	s.mu.Unlock()
	s.logger.Error("index store stopped after write failure", "error", err)
	select {
	case s.flushDoneCh <- struct{}{}:
	default:
	}
}

func (s *PebbleStore) Failed() <-chan struct{} { return s.failed }

func (s *PebbleStore) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.terminalErr
}

func (s *PebbleStore) SaveProgress(progress string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminalErr != nil {
		return s.terminalErr
	}
	if s.closed {
		return errors.New("index store is closed")
	}
	s.pendingProgress = progress
	return nil
}

// executeIndexDelete performs the actual index deletion.
func (s *PebbleStore) executeIndexDelete(delOp indexDeleteOp) error {
	// Delete all idx entries
	idxPrefix := indexKeyPrefix(delOp.db, delOp.pattern, delOp.tmplID)
	if err := s.deleteByPrefix(idxPrefix); err != nil {
		return fmt.Errorf("failed to delete index entries: %w", err)
	}

	// Delete all rev entries
	revPrefix := reverseKeyPrefix(delOp.db, delOp.pattern, delOp.tmplID)
	if err := s.deleteByPrefix(revPrefix); err != nil {
		return fmt.Errorf("failed to delete reverse entries: %w", err)
	}

	// Delete map entry
	mKey := mapKey(delOp.db, delOp.pattern, delOp.tmplID)
	if err := s.db.Delete(mKey, pebble.Sync); err != nil && err != pebble.ErrNotFound {
		return fmt.Errorf("failed to delete map entry: %w", err)
	}

	// Delete state entry
	sKey := stateKey(delOp.db, delOp.pattern, delOp.tmplID)
	if err := s.db.Delete(sKey, pebble.Sync); err != nil && err != pebble.ErrNotFound {
		return fmt.Errorf("failed to delete state entry: %w", err)
	}

	return nil
}

// applyOp applies a single pending operation to the batch.
func (s *PebbleStore) applyOp(batch Batch, op *pendingOp) error {
	if op.orderKey == nil {
		// Delete operation
		// Need to look up existing orderKey to delete idx entry
		oldOrderKey, closer, err := s.db.Get(op.revKey)
		if err == pebble.ErrNotFound {
			return nil // Already deleted, idempotent
		}
		if err != nil {
			return fmt.Errorf("failed to read reverse index: %w", err)
		}
		closer.Close()

		// Delete index entry
		oldIdxKey := indexKey(op.db, op.pattern, op.tmplID, oldOrderKey)
		if err := batch.Delete(oldIdxKey, nil); err != nil {
			return fmt.Errorf("failed to delete index entry: %w", err)
		}

		// Delete reverse index
		if err := batch.Delete(op.revKey, nil); err != nil {
			return fmt.Errorf("failed to delete reverse index: %w", err)
		}
	} else {
		// Upsert operation
		// First delete old index entry if exists
		oldOrderKey, closer, err := s.db.Get(op.revKey)
		if err != nil && err != pebble.ErrNotFound {
			return fmt.Errorf("failed to read reverse index: %w", err)
		}
		if closer != nil {
			if oldOrderKey != nil {
				oldIdxKey := indexKey(op.db, op.pattern, op.tmplID, oldOrderKey)
				if err := batch.Delete(oldIdxKey, nil); err != nil {
					closer.Close()
					return fmt.Errorf("failed to delete old index entry: %w", err)
				}
			}
			closer.Close()
		}

		// Set new index entry
		if err := batch.Set(op.idxKey, []byte(op.docID), nil); err != nil {
			return fmt.Errorf("failed to set index entry: %w", err)
		}

		// Set reverse index
		if err := batch.Set(op.revKey, op.orderKey, nil); err != nil {
			return fmt.Errorf("failed to set reverse index: %w", err)
		}

		// Set map entry
		mapValue := op.pattern + "|" + op.tmplID
		if err := batch.Set(op.mapKey, []byte(mapValue), nil); err != nil {
			return fmt.Errorf("failed to set map entry: %w", err)
		}
	}
	return nil
}

// Upsert inserts or updates a document in the index.
// Uses async batching for better throughput.
func (s *PebbleStore) Upsert(db, pattern, tmplID, docID string, orderKey []byte, progress string) error {
	hash := indexHash(pattern, tmplID)
	idxMapKey := db + "|" + hash

	// Check if this index is pending deletion - skip the upsert but still update progress
	s.mu.Lock()
	if s.terminalErr != nil {
		err := s.terminalErr
		s.mu.Unlock()
		return err
	}
	if s.closed {
		s.mu.Unlock()
		return errors.New("index store is closed")
	}
	if s.indexDeletingLocked(idxMapKey) {
		// Index is being deleted, only update progress if provided
		if progress != "" {
			s.pendingProgress = progress
		}
		s.mu.Unlock()
		return nil
	}

	// Make a copy of orderKey
	orderKeyCopy := make([]byte, len(orderKey))
	copy(orderKeyCopy, orderKey)

	op := &pendingOp{
		db:       db,
		pattern:  pattern,
		tmplID:   tmplID,
		hash:     hash,
		docID:    docID,
		orderKey: orderKeyCopy,
		idxKey:   indexKey(db, pattern, tmplID, orderKeyCopy),
		revKey:   reverseKey(db, pattern, tmplID, docID),
		mapKey:   mapKey(db, pattern, tmplID),
	}

	// Get or create the inner map for this index
	inner, ok := s.pending[idxMapKey]
	if !ok {
		inner = make(map[string]*pendingOp)
		s.pending[idxMapKey] = inner
	}
	inner[docID] = op

	if progress != "" {
		s.pendingProgress = progress
	}

	// Count total pending ops
	pendingCount := 0
	for _, m := range s.pending {
		pendingCount += len(m)
	}
	s.mu.Unlock()

	// Notify batcher if batch size reached
	if pendingCount >= s.batchSize {
		select {
		case s.notifyCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// Delete removes a document from the index.
// Uses async batching for better throughput.
func (s *PebbleStore) Delete(db, pattern, tmplID, docID string, progress string) error {
	hash := indexHash(pattern, tmplID)
	idxMapKey := db + "|" + hash

	// Check if this index is pending deletion - skip the delete but still update progress
	s.mu.Lock()
	if s.terminalErr != nil {
		err := s.terminalErr
		s.mu.Unlock()
		return err
	}
	if s.closed {
		s.mu.Unlock()
		return errors.New("index store is closed")
	}
	if s.indexDeletingLocked(idxMapKey) {
		// Index is being deleted, only update progress if provided
		if progress != "" {
			s.pendingProgress = progress
		}
		s.mu.Unlock()
		return nil
	}

	op := &pendingOp{
		db:       db,
		pattern:  pattern,
		tmplID:   tmplID,
		hash:     hash,
		docID:    docID,
		orderKey: nil, // nil indicates delete
		revKey:   reverseKey(db, pattern, tmplID, docID),
	}

	// Get or create the inner map for this index
	inner, ok := s.pending[idxMapKey]
	if !ok {
		inner = make(map[string]*pendingOp)
		s.pending[idxMapKey] = inner
	}
	inner[docID] = op

	if progress != "" {
		s.pendingProgress = progress
	}

	// Count total pending ops
	pendingCount := 0
	for _, m := range s.pending {
		pendingCount += len(m)
	}
	s.mu.Unlock()

	// Notify batcher if batch size reached
	if pendingCount >= s.batchSize {
		select {
		case s.notifyCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// Get returns the OrderKey for a document by ID.
// It checks pending operations first, then falls back to the database.
func (s *PebbleStore) Get(db, pattern, tmplID, docID string) ([]byte, bool) {
	hash := indexHash(pattern, tmplID)
	idxMapKey := db + "|" + hash

	// Check if this index is pending deletion
	s.mu.RLock()
	if s.indexDeletingLocked(idxMapKey) {
		s.mu.RUnlock()
		return nil, false // Index is being deleted
	}

	// Check pending operations first (newest to oldest)
	// Check pending first (newest), then flushing (older)
	if inner, ok := s.pending[idxMapKey]; ok {
		if op, found := inner[docID]; found {
			s.mu.RUnlock()
			if op.orderKey == nil {
				return nil, false // Pending delete
			}
			result := make([]byte, len(op.orderKey))
			copy(result, op.orderKey)
			return result, true
		}
	}
	if inner, ok := s.flushing[idxMapKey]; ok {
		if op, found := inner[docID]; found {
			s.mu.RUnlock()
			if op.orderKey == nil {
				return nil, false // Pending delete
			}
			result := make([]byte, len(op.orderKey))
			copy(result, op.orderKey)
			return result, true
		}
	}
	s.mu.RUnlock()

	// Fall back to database
	revKey := reverseKey(db, pattern, tmplID, docID)
	orderKey, closer, err := s.db.Get(revKey)
	if err != nil {
		return nil, false
	}
	defer closer.Close()

	// Make a copy since the returned slice is only valid until closer.Close()
	result := make([]byte, len(orderKey))
	copy(result, orderKey)
	return result, true
}

// Search returns documents within the specified bounds.
// It merges pending operations with database results using streaming merge.
func (s *PebbleStore) Search(db, pattern, tmplID string, opts store.SearchOptions) ([]store.DocRef, error) {
	hash := indexHash(pattern, tmplID)
	idxMapKey := db + "|" + hash

	// Check if this index is pending deletion
	s.mu.RLock()
	if s.indexDeletingLocked(idxMapKey) {
		s.mu.RUnlock()
		return nil, nil // Index is being deleted, return empty
	}

	// Snapshot pending operations for this index using O(1) lookup
	memOps := make(map[string]*pendingOp)
	// Copy flushing first (older)
	if inner, ok := s.flushing[idxMapKey]; ok {
		for docID, op := range inner {
			memOps[docID] = op
		}
	}
	// Copy pending (newer overwrites)
	if inner, ok := s.pending[idxMapKey]; ok {
		for docID, op := range inner {
			memOps[docID] = op
		}
	}
	s.mu.RUnlock()

	prefix := indexKeyPrefix(db, pattern, tmplID)

	// Build lower and upper bounds for iterator
	var lower, upper []byte
	if opts.Lower != nil {
		lower = append(prefix, opts.Lower...)
	} else {
		lower = prefix
	}
	if opts.Upper != nil {
		upper = append(prefix, opts.Upper...)
	} else {
		upper = make([]byte, len(prefix))
		copy(upper, prefix)
		upper[len(upper)-1]++
	}

	// Handle StartAfter for pagination
	if opts.StartAfter != nil {
		startAfterKey := append(prefix, opts.StartAfter...)
		if len(lower) == 0 || string(startAfterKey) > string(lower) {
			lower = startAfterKey
		}
	}

	iterOpts := &pebble.IterOptions{
		LowerBound: lower,
		UpperBound: upper,
	}

	iter, err := s.db.NewIter(iterOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create iterator: %w", err)
	}
	defer iter.Close()

	limit := opts.Limit
	if limit <= 0 {
		limit = 1000
	}

	// Fast path: no pending operations, just stream from iterator
	if len(memOps) == 0 {
		return s.searchFastPath(iter, prefix, opts, limit)
	}

	// Slow path: merge DB results with pending operations using heap
	return s.searchWithPending(iter, prefix, opts, limit, memOps)
}

// searchFastPath handles search when there are no pending operations.
// It streams directly from the iterator, stopping at limit.
func (s *PebbleStore) searchFastPath(iter Iterator, prefix []byte, opts store.SearchOptions, limit int) ([]store.DocRef, error) {
	results := make([]store.DocRef, 0, limit)

	for iter.First(); iter.Valid() && len(results) < limit; iter.Next() {
		key := iter.Key()

		// Skip StartAfter cursor itself (exclusive)
		if opts.StartAfter != nil {
			orderKey := parseIndexKey(key, prefix)
			if len(orderKey) > 0 && bytes.Equal(orderKey, opts.StartAfter) {
				continue
			}
		}

		orderKey := parseIndexKey(key, prefix)
		docID := string(iter.Value())

		results = append(results, store.DocRef{
			ID:       docID,
			OrderKey: append([]byte(nil), orderKey...),
		})
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %w", err)
	}

	return results, nil
}

// searchWithPending merges DB results with pending operations using a heap.
func (s *PebbleStore) searchWithPending(iter Iterator, prefix []byte, opts store.SearchOptions, limit int, memOps map[string]*pendingOp) ([]store.DocRef, error) {
	// Build sorted slice of pending inserts/updates that are in bounds
	pendingDocs := make([]store.DocRef, 0, len(memOps))
	pendingDeletes := make(map[string]bool)

	for docID, op := range memOps {
		if op.orderKey == nil {
			pendingDeletes[docID] = true
			continue
		}
		if isInBounds(op.orderKey, opts) {
			pendingDocs = append(pendingDocs, store.DocRef{
				ID:       docID,
				OrderKey: append([]byte(nil), op.orderKey...),
			})
		}
	}

	// Sort pending docs by orderKey
	sort.Slice(pendingDocs, func(i, j int) bool {
		return bytes.Compare(pendingDocs[i].OrderKey, pendingDocs[j].OrderKey) < 0
	})

	// Merge: iterate DB and pending simultaneously
	results := make([]store.DocRef, 0, limit)
	seenDocs := make(map[string]bool)
	pendingIdx := 0
	iterValid := iter.First()

	for len(results) < limit {
		// Get current DB entry (skip deleted/updated docs)
		var dbDoc *store.DocRef
		for iterValid {
			key := iter.Key()
			orderKey := parseIndexKey(key, prefix)
			docID := string(iter.Value())

			// Skip StartAfter cursor
			if opts.StartAfter != nil && bytes.Equal(orderKey, opts.StartAfter) {
				iterValid = iter.Next()
				continue
			}

			// Skip if pending delete or update (will be handled from pendingDocs)
			if _, hasPending := memOps[docID]; hasPending {
				seenDocs[docID] = true
				iterValid = iter.Next()
				continue
			}

			if !seenDocs[docID] {
				dbDoc = &store.DocRef{
					ID:       docID,
					OrderKey: append([]byte(nil), orderKey...),
				}
				break
			}
			iterValid = iter.Next()
		}

		// Get current pending entry (skip already seen)
		var pendingDoc *store.DocRef
		for pendingIdx < len(pendingDocs) {
			if !seenDocs[pendingDocs[pendingIdx].ID] {
				pendingDoc = &pendingDocs[pendingIdx]
				break
			}
			pendingIdx++
		}

		// Pick smaller one
		if dbDoc == nil && pendingDoc == nil {
			break
		}

		if dbDoc == nil {
			seenDocs[pendingDoc.ID] = true
			results = append(results, *pendingDoc)
			pendingIdx++
		} else if pendingDoc == nil {
			seenDocs[dbDoc.ID] = true
			results = append(results, *dbDoc)
			iterValid = iter.Next()
		} else if bytes.Compare(dbDoc.OrderKey, pendingDoc.OrderKey) <= 0 {
			seenDocs[dbDoc.ID] = true
			results = append(results, *dbDoc)
			iterValid = iter.Next()
		} else {
			seenDocs[pendingDoc.ID] = true
			results = append(results, *pendingDoc)
			pendingIdx++
		}
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("iterator error: %w", err)
	}

	return results, nil
}

// isInBounds checks if orderKey is within the search bounds.
func isInBounds(orderKey []byte, opts store.SearchOptions) bool {
	if opts.Lower != nil && bytes.Compare(orderKey, opts.Lower) < 0 {
		return false
	}
	if opts.Upper != nil && bytes.Compare(orderKey, opts.Upper) >= 0 {
		return false
	}
	if opts.StartAfter != nil && bytes.Compare(orderKey, opts.StartAfter) <= 0 {
		return false
	}
	return true
}

func (s *PebbleStore) indexDeletingLocked(key string) bool {
	_, pending := s.pendingIndexDeletes[key]
	_, flushing := s.flushingIndexDeletes[key]
	return pending || flushing
}

// DeleteIndex removes all data for an index asynchronously.
// The deletion is queued and processed by the batcher.
func (s *PebbleStore) DeleteIndex(db, pattern, tmplID string) error {
	hash := indexHash(pattern, tmplID)
	idxMapKey := db + "|" + hash

	s.mu.Lock()
	// 1. Clear any pending operations for this index (O(1) lookup)
	delete(s.pending, idxMapKey)

	// 2. Queue the index delete operation
	s.pendingIndexDeletes[idxMapKey] = indexDeleteOp{
		db:      db,
		pattern: pattern,
		tmplID:  tmplID,
		hash:    hash,
	}
	s.mu.Unlock()

	// 3. Trigger flush to process the delete
	select {
	case s.notifyCh <- struct{}{}:
	default:
	}

	return nil
}

// deleteByPrefix deletes all keys with the given prefix.
func (s *PebbleStore) deleteByPrefix(prefix []byte) error {
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := s.db.NewBatch()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := batch.Delete(iter.Key(), nil); err != nil {
			batch.Close()
			return err
		}
	}

	if err := iter.Error(); err != nil {
		batch.Close()
		return err
	}

	if err := batch.Commit(pebble.Sync); err != nil {
		batch.Close()
		return err
	}

	return batch.Close()
}

// SetState sets the state of an index.
func (s *PebbleStore) SetState(db, pattern, tmplID string, state store.IndexState) error {
	sKey := stateKey(db, pattern, tmplID)
	return s.db.Set(sKey, []byte(state), pebble.Sync)
}

// GetState returns the state of an index.
func (s *PebbleStore) GetState(db, pattern, tmplID string) (store.IndexState, error) {
	sKey := stateKey(db, pattern, tmplID)
	value, closer, err := s.db.Get(sKey)
	if err == pebble.ErrNotFound {
		return store.IndexStateHealthy, nil // Default to healthy
	}
	if err != nil {
		return "", fmt.Errorf("failed to get state: %w", err)
	}
	defer closer.Close()

	return store.IndexState(value), nil
}

// LoadProgress loads the event processing progress.
// Returns the pending progress if available, otherwise reads from disk.
func (s *PebbleStore) LoadProgress() (string, error) {
	// Check pending progress first (most recent)
	s.mu.RLock()
	pendingProg := s.pendingProgress
	err := s.terminalErr
	s.mu.RUnlock()
	if err != nil {
		return "", err
	}
	if pendingProg != "" {
		return pendingProg, nil
	}

	// Fall back to persisted progress
	value, closer, err := s.db.Get([]byte(keyProgress))
	if err == pebble.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to load progress: %w", err)
	}
	defer closer.Close()

	return string(value), nil
}

// ListDatabases returns all database names.
func (s *PebbleStore) ListDatabases() ([]string, error) {
	dbSet := make(map[string]struct{})

	// 1. Collect from pending operations
	s.mu.RLock()
	for _, inner := range s.flushing {
		for _, op := range inner {
			if op.orderKey != nil { // Only upserts, not deletes
				dbSet[op.db] = struct{}{}
			}
		}
	}
	for _, inner := range s.pending {
		for _, op := range inner {
			if op.orderKey != nil {
				dbSet[op.db] = struct{}{}
			}
		}
	}
	s.mu.RUnlock()

	// 2. Collect from persisted data
	prefix := []byte(prefixMap)
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err != nil {
		s.logger.Error("failed to create iterator for ListDatabases", "error", err)
		return nil, err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		// Key format: map/{db}/{hash}
		// Skip "map/" prefix (4 chars)
		remainder := key[len(prefix):]
		// Find first '/' to extract db name
		for i, b := range remainder {
			if b == '/' {
				dbName, err := decodePathComponent(string(remainder[:i]))
				if err == nil {
					dbSet[dbName] = struct{}{}
				}
				break
			}
		}
	}

	result := make([]string, 0, len(dbSet))
	for db := range dbSet {
		result = append(result, db)
	}
	return result, nil
}

// ListIndexes returns metadata for all indexes in a database.
func (s *PebbleStore) ListIndexes(db string) ([]store.IndexInfo, error) {
	// indexKey is pattern + "|" + tmplID
	indexSet := make(map[string]struct {
		pattern string
		tmplID  string
	})

	// 1. Collect from pending operations
	s.mu.RLock()
	pendingDeletes := make(map[string]bool)
	for key := range s.pendingIndexDeletes {
		pendingDeletes[key] = true
	}
	for key := range s.flushingIndexDeletes {
		pendingDeletes[key] = true
	}
	for _, inner := range s.flushing {
		for _, op := range inner {
			if op.db == db && op.orderKey != nil { // Only upserts for this db
				key := op.pattern + "|" + op.tmplID
				indexSet[key] = struct {
					pattern string
					tmplID  string
				}{op.pattern, op.tmplID}
			}
		}
	}
	for _, inner := range s.pending {
		for _, op := range inner {
			if op.db == db && op.orderKey != nil {
				key := op.pattern + "|" + op.tmplID
				indexSet[key] = struct {
					pattern string
					tmplID  string
				}{op.pattern, op.tmplID}
			}
		}
	}
	s.mu.RUnlock()

	// 2. Collect from persisted data
	encodedDB := encodePathComponent(db)
	prefix := []byte(prefixMap + encodedDB + "/")
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err != nil {
		s.logger.Error("failed to create iterator for ListIndexes", "error", err)
		return nil, err
	} else {
		defer iter.Close()
		for iter.First(); iter.Valid(); iter.Next() {
			value := iter.Value()
			// Value format: {pattern}|{tmplID}
			parts := bytes.SplitN(value, []byte("|"), 2)
			if len(parts) != 2 {
				continue
			}
			pattern := string(parts[0])
			tmplID := string(parts[1])
			key := pattern + "|" + tmplID
			indexSet[key] = struct {
				pattern string
				tmplID  string
			}{pattern, tmplID}
		}
	}

	// 3. Build results, filtering out pending deletes
	s.mu.RLock()
	for delKey := range s.pendingIndexDeletes {
		pendingDeletes[delKey] = true
	}
	for delKey := range s.flushingIndexDeletes {
		pendingDeletes[delKey] = true
	}
	s.mu.RUnlock()

	var results []store.IndexInfo
	for _, idx := range indexSet {
		// Check if this index is pending deletion
		hash := indexHash(idx.pattern, idx.tmplID)
		delKey := db + "|" + hash
		if pendingDeletes[delKey] {
			continue // Skip pending deletes
		}

		// Get state
		state, _ := s.GetState(db, idx.pattern, idx.tmplID)

		// Count documents
		docCount := s.countDocs(db, idx.pattern, idx.tmplID)

		results = append(results, store.IndexInfo{
			Pattern:    idx.pattern,
			TemplateID: idx.tmplID,
			RawPattern: idx.pattern,
			State:      state,
			DocCount:   docCount,
		})
	}

	return results, nil
}

// DeleteDatabase removes all indexes for a database.
func (s *PebbleStore) DeleteDatabase(db string) error {
	// 1. List all indexes for this database
	indexes, err := s.ListIndexes(db)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}

	// 2. Delete each index
	for _, idx := range indexes {
		if err := s.DeleteIndex(db, idx.Pattern, idx.TemplateID); err != nil {
			s.logger.Error("failed to delete index during database deletion",
				"db", db,
				"pattern", idx.Pattern,
				"tmplID", idx.TemplateID,
				"error", err)
			// Continue with other indexes
		}
	}

	return nil
}

// countDocs counts the number of documents in an index.
func (s *PebbleStore) countDocs(db, pattern, tmplID string) int {
	hash := indexHash(pattern, tmplID)
	idxMapKey := db + "|" + hash
	docSet := make(map[string]bool) // true = exists, false = deleted

	// 1. Count from pending operations (O(1) lookup for the index)
	s.mu.RLock()
	if inner, ok := s.flushing[idxMapKey]; ok {
		for docID, op := range inner {
			docSet[docID] = op.orderKey != nil // nil = delete
		}
	}
	if inner, ok := s.pending[idxMapKey]; ok {
		for docID, op := range inner {
			docSet[docID] = op.orderKey != nil
		}
	}
	s.mu.RUnlock()

	// 2. Count from persisted data
	prefix := reverseKeyPrefix(db, pattern, tmplID)
	upper := make([]byte, len(prefix))
	copy(upper, prefix)
	upper[len(upper)-1]++

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	})
	if err == nil {
		defer iter.Close()
		for iter.First(); iter.Valid(); iter.Next() {
			// Extract docID from key: rev/{db}/{hash}/{docID}
			key := iter.Key()
			docID := string(key[len(prefix):])
			if _, seen := docSet[docID]; !seen {
				docSet[docID] = true
			}
		}
	}

	// 3. Count non-deleted docs
	count := 0
	for _, exists := range docSet {
		if exists {
			count++
		}
	}
	return count
}

// Compile-time check that PebbleStore implements store.Store.
var _ store.Store = (*PebbleStore)(nil)
