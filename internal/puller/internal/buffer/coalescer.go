package buffer

import (
	"sort"

	"github.com/codetrek/syntrix/internal/puller/events"
)

// Coalescer merges events for the same document during catch-up.
type Coalescer struct {
	// pending maps document key to pending event
	pending map[string]*events.ChangeEvent
}

// NewCoalescer creates a new coalescer.
func NewCoalescer() *Coalescer {
	return &Coalescer{
		pending: make(map[string]*events.ChangeEvent),
	}
}

// docKey creates a unique key for a document.
func docKey(collection, documentID string) string {
	return collection + "/" + documentID
}

// Add adds an event to be coalesced.
// Returns the coalesced event if it should be emitted immediately, or nil if pending.
func (c *Coalescer) Add(evt *events.ChangeEvent) *events.ChangeEvent {
	key := docKey(evt.MgoColl, evt.MgoDocID)

	existing, ok := c.pending[key]
	if !ok {
		// First event for this document
		c.pending[key] = evt
		return nil
	}

	// Merge based on operation types
	merged := c.merge(existing, evt)
	if merged == nil {
		// Events cancelled out (insert + delete)
		delete(c.pending, key)
		return nil
	}

	c.pending[key] = merged
	return nil
}

// Flush returns all pending events and clears the coalescer.
func (c *Coalescer) Flush() []*events.ChangeEvent {
	if len(c.pending) == 0 {
		return nil
	}

	result := make([]*events.ChangeEvent, 0, len(c.pending))
	for _, evt := range c.pending {
		result = append(result, evt)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].BufferKey() < result[j].BufferKey()
	})

	c.pending = make(map[string]*events.ChangeEvent)
	return result
}

// FlushOne removes and returns a single pending event.
// Returns the document key and event, or empty values if no events pending.
func (c *Coalescer) FlushOne(collection, documentID string) *events.ChangeEvent {
	key := docKey(collection, documentID)
	evt, ok := c.pending[key]
	if !ok {
		return nil
	}
	delete(c.pending, key)
	return evt
}

// Count returns the number of pending documents.
func (c *Coalescer) Count() int {
	return len(c.pending)
}

// Clear removes all pending events.
func (c *Coalescer) Clear() {
	c.pending = make(map[string]*events.ChangeEvent)
}

// merge merges two events according to coalescing rules:
// - update + update → keep latest update
// - insert + update → insert with updated data
// - insert + delete → nil (cancel out)
// - update + delete → keep delete
// - replace + any → treat replace like update
// - delete always wins
func (c *Coalescer) merge(existing, incoming *events.ChangeEvent) *events.ChangeEvent {
	// Delete always wins
	if incoming.OpType == events.OperationDelete {
		// If previous was insert, they cancel out
		if existing.OpType == events.OperationInsert {
			return nil
		}
		// Otherwise, keep the delete
		return incoming
	}

	// If existing is delete, new event wins (shouldn't happen in practice)
	if existing.OpType == events.OperationDelete {
		return incoming
	}

	// insert + update = insert with updated data
	if existing.OpType == events.OperationInsert {
		if incoming.OpType == events.OperationUpdate || incoming.OpType == events.OperationReplace {
			// Create insert with the full document from update
			merged := &events.ChangeEvent{
				EventID:      incoming.EventID, // Use latest event ID
				TenantID:     incoming.TenantID,
				MgoColl:      incoming.MgoColl,
				MgoDocID:     incoming.MgoDocID,
				OpType:       events.OperationInsert, // Keep as insert
				ClusterTime:  incoming.ClusterTime,
				Timestamp:    incoming.Timestamp,
				FullDocument: incoming.FullDocument,
				TxnNumber:    incoming.TxnNumber,
				// Don't include UpdateDesc since this is now an insert
			}
			return merged
		}
		// insert + insert = keep latest (shouldn't happen but handle gracefully)
		return incoming
	}

	// update/replace + update/replace = keep latest
	if (existing.OpType == events.OperationUpdate || existing.OpType == events.OperationReplace) &&
		(incoming.OpType == events.OperationUpdate || incoming.OpType == events.OperationReplace) {
		return incoming
	}

	// Default: keep incoming
	return incoming
}

// CoalesceEvents coalesces a batch of events for the same document.
// Returns the coalesced events.
func CoalesceEvents(evts []*events.ChangeEvent) []*events.ChangeEvent {
	if len(evts) == 0 {
		return nil
	}

	c := NewCoalescer()
	for _, evt := range evts {
		c.Add(evt)
	}
	return c.Flush()
}
