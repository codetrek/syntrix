package buffer

import (
	"github.com/codetrek/syntrix/internal/events"
)

// Coalescer merges events for the same document during catch-up.
type Coalescer struct {
	// pending maps document key to pending event
	pending map[string]*events.NormalizedEvent
}

// NewCoalescer creates a new coalescer.
func NewCoalescer() *Coalescer {
	return &Coalescer{
		pending: make(map[string]*events.NormalizedEvent),
	}
}

// docKey creates a unique key for a document.
func docKey(collection, documentID string) string {
	return collection + "/" + documentID
}

// Add adds an event to be coalesced.
// Returns the coalesced event if it should be emitted immediately, or nil if pending.
func (c *Coalescer) Add(evt *events.NormalizedEvent) *events.NormalizedEvent {
	key := docKey(evt.Collection, evt.DocumentID)

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
func (c *Coalescer) Flush() []*events.NormalizedEvent {
	if len(c.pending) == 0 {
		return nil
	}

	result := make([]*events.NormalizedEvent, 0, len(c.pending))
	for _, evt := range c.pending {
		result = append(result, evt)
	}

	c.pending = make(map[string]*events.NormalizedEvent)
	return result
}

// FlushOne removes and returns a single pending event.
// Returns the document key and event, or empty values if no events pending.
func (c *Coalescer) FlushOne(collection, documentID string) *events.NormalizedEvent {
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
	c.pending = make(map[string]*events.NormalizedEvent)
}

// merge merges two events according to coalescing rules:
// - update + update → keep latest update
// - insert + update → insert with updated data
// - insert + delete → nil (cancel out)
// - update + delete → keep delete
// - replace + any → treat replace like update
// - delete always wins
func (c *Coalescer) merge(existing, incoming *events.NormalizedEvent) *events.NormalizedEvent {
	// Delete always wins
	if incoming.Type == events.OperationDelete {
		// If previous was insert, they cancel out
		if existing.Type == events.OperationInsert {
			return nil
		}
		// Otherwise, keep the delete
		return incoming
	}

	// If existing is delete, new event wins (shouldn't happen in practice)
	if existing.Type == events.OperationDelete {
		return incoming
	}

	// insert + update = insert with updated data
	if existing.Type == events.OperationInsert {
		if incoming.Type == events.OperationUpdate || incoming.Type == events.OperationReplace {
			// Create insert with the full document from update
			merged := &events.NormalizedEvent{
				EventID:      incoming.EventID, // Use latest event ID
				TenantID:     incoming.TenantID,
				Collection:   incoming.Collection,
				DocumentID:   incoming.DocumentID,
				Type:         events.OperationInsert, // Keep as insert
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
	if (existing.Type == events.OperationUpdate || existing.Type == events.OperationReplace) &&
		(incoming.Type == events.OperationUpdate || incoming.Type == events.OperationReplace) {
		return incoming
	}

	// Default: keep incoming
	return incoming
}

// CoalesceEvents coalesces a batch of events for the same document.
// Returns the coalesced events.
func CoalesceEvents(evts []*events.NormalizedEvent) []*events.NormalizedEvent {
	if len(evts) == 0 {
		return nil
	}

	c := NewCoalescer()
	for _, evt := range evts {
		c.Add(evt)
	}
	return c.Flush()
}
