package buffer

import (
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"sort"
)

type documentKey struct{ collection, document string }
type coalesced struct {
	event *events.StoreChangeEvent
	order uint64
}

// Coalescer preserves the order of each document's final occurrence in an
// already ordered source window. Only the whole window may be acknowledged.
type Coalescer struct {
	pending map[documentKey]coalesced
	order   uint64
}

func NewCoalescer() *Coalescer                       { return &Coalescer{pending: make(map[documentKey]coalesced)} }
func docKey(collection, document string) documentKey { return documentKey{collection, document} }
func (c *Coalescer) Add(event *events.StoreChangeEvent) *events.StoreChangeEvent {
	key := docKey(event.MgoColl, event.MgoDocID)
	c.order++
	if previous, ok := c.pending[key]; ok {
		event = c.merge(previous.event, event)
	}
	if event == nil {
		delete(c.pending, key)
		return nil
	}
	c.pending[key] = coalesced{event: event, order: c.order}
	return nil
}
func (c *Coalescer) Flush() []*events.StoreChangeEvent {
	entries := make([]coalesced, 0, len(c.pending))
	for _, entry := range c.pending {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
	var result []*events.StoreChangeEvent
	for _, entry := range entries {
		result = append(result, entry.event)
	}
	c.Clear()
	return result
}
func (c *Coalescer) FlushOne(collection, document string) *events.StoreChangeEvent {
	key := docKey(collection, document)
	entry := c.pending[key]
	delete(c.pending, key)
	return entry.event
}
func (c *Coalescer) Count() int { return len(c.pending) }
func (c *Coalescer) Clear()     { clear(c.pending); c.order = 0 }
func (c *Coalescer) merge(existing, incoming *events.StoreChangeEvent) *events.StoreChangeEvent {
	if incoming.OpType == events.StoreOperationDelete {
		if existing.OpType == events.StoreOperationInsert {
			return nil
		}
		return incoming
	}
	if existing.OpType == events.StoreOperationInsert && (incoming.OpType == events.StoreOperationUpdate || incoming.OpType == events.StoreOperationReplace) {
		merged := *incoming
		merged.OpType = events.StoreOperationInsert
		merged.UpdateDesc = nil
		return &merged
	}
	return incoming
}
func CoalesceEvents(input []*events.StoreChangeEvent) []*events.StoreChangeEvent {
	coalescer := NewCoalescer()
	for _, event := range input {
		coalescer.Add(event)
	}
	return coalescer.Flush()
}
