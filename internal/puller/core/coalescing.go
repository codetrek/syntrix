package core

import (
	"slices"

	"github.com/syntrixbase/syntrix/internal/puller/buffer"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
)

// The entire bounded page is one coalescing window. Intermediate outputs carry
// the preceding checkpoint; only its last output acknowledges the window.
// Terminal deletes survive retry windows that previously delivered an insert.
func coalescedDeliveries(records []buffer.Record, through cursor.Position) []delivery {
	type documentKey struct{ collection, id string }
	pending := make(map[documentKey]buffer.Record)
	for _, record := range records {
		key := documentKey{record.Event.MgoColl, record.Event.MgoDocID}
		previous, exists := pending[key]
		if !exists {
			pending[key] = record
			continue
		}
		if previous.Event.OpType == events.StoreOperationInsert && (record.Event.OpType == events.StoreOperationUpdate || record.Event.OpType == events.StoreOperationReplace) {
			event := *record.Event
			event.OpType = events.StoreOperationInsert
			event.UpdateDesc = nil
			record.Event = &event
		}
		pending[key] = record
	}
	records = make([]buffer.Record, 0, len(pending))
	for _, record := range pending {
		records = append(records, record)
	}
	slices.SortFunc(records, func(a, b buffer.Record) int {
		if a.Position.Sequence < b.Position.Sequence {
			return -1
		}
		if a.Position.Sequence > b.Position.Sequence {
			return 1
		}
		return 0
	})
	result := make([]delivery, 0, max(1, len(records)))
	for _, record := range records {
		result = append(result, delivery{event: record.Event})
	}
	if len(result) == 0 {
		result = append(result, delivery{})
	}
	result[len(result)-1].position = through
	result[len(result)-1].advance = true
	return result
}
