package buffer

import (
	"testing"

	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/storage"
)

func TestCoalescer_Add_FirstEvent(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	evt := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}

	result := c.Add(evt)
	if result != nil {
		t.Error("Add() should return nil for first event")
	}

	if c.Count() != 1 {
		t.Errorf("Count() = %d, want 1", c.Count())
	}
}

func TestCoalescer_InsertThenDelete(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	insert := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}
	c.Add(insert)

	delete := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationDelete,
	}
	c.Add(delete)

	// Should cancel out
	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0 (events should cancel out)", c.Count())
	}

	flushed := c.Flush()
	if len(flushed) != 0 {
		t.Errorf("Flush() returned %d events, want 0", len(flushed))
	}
}

func TestCoalescer_InsertThenUpdate(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	insert := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
		FullDocument: &storage.Document{
			Data: map[string]any{"name": "original"},
		},
	}
	c.Add(insert)

	update := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationUpdate,
		FullDocument: &storage.Document{
			Data: map[string]any{"name": "updated"},
		},
	}
	c.Add(update)

	// Should keep as insert with updated data
	if c.Count() != 1 {
		t.Errorf("Count() = %d, want 1", c.Count())
	}

	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}

	if flushed[0].OpType != events.OperationInsert {
		t.Errorf("OpType = %s, want insert", flushed[0].OpType)
	}
	if flushed[0].FullDocument.Data["name"] != "updated" {
		t.Errorf("FullDocument.Data['name'] = %v, want 'updated'", flushed[0].FullDocument.Data["name"])
	}
}

func TestCoalescer_UpdateThenUpdate(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	update1 := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationUpdate,
		FullDocument: &storage.Document{
			Data: map[string]any{"name": "first"},
		},
	}
	c.Add(update1)

	update2 := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationUpdate,
		FullDocument: &storage.Document{
			Data: map[string]any{"name": "second"},
		},
	}
	c.Add(update2)

	// Should keep latest
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}

	if flushed[0].EventID != "evt-2" {
		t.Errorf("EventID = %s, want evt-2", flushed[0].EventID)
	}
}

func TestCoalescer_UpdateThenDelete(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	update := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationUpdate,
	}
	c.Add(update)

	delete := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationDelete,
	}
	c.Add(delete)

	// Should keep delete
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}

	if flushed[0].OpType != events.OperationDelete {
		t.Errorf("OpType = %s, want delete", flushed[0].OpType)
	}
}

func TestCoalescer_FlushOne(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	evt1 := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "coll-a",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}
	evt2 := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "coll-b",
		MgoDocID: "doc-2",
		OpType:   events.OperationInsert,
	}
	c.Add(evt1)
	c.Add(evt2)

	flushed := c.FlushOne("coll-a", "doc-1")
	if flushed == nil {
		t.Fatal("FlushOne() returned nil")
	}
	if flushed.EventID != "evt-1" {
		t.Errorf("EventID = %s, want evt-1", flushed.EventID)
	}

	if c.Count() != 1 {
		t.Errorf("Count() = %d, want 1", c.Count())
	}

	// Non-existent
	flushed = c.FlushOne("coll-a", "doc-1")
	if flushed != nil {
		t.Error("FlushOne() should return nil for non-existent")
	}
}

func TestCoalescer_Clear(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	evt := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}
	c.Add(evt)

	c.Clear()
	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0", c.Count())
	}
}

func TestCoalesceEvents(t *testing.T) {
	t.Parallel()
	evts := []*events.ChangeEvent{
		{
			EventID:  "evt-1",
			MgoColl:  "testcoll",
			MgoDocID: "doc-1",
			OpType:   events.OperationInsert,
		},
		{
			EventID:  "evt-2",
			MgoColl:  "testcoll",
			MgoDocID: "doc-1",
			OpType:   events.OperationDelete,
		},
		{
			EventID:  "evt-3",
			MgoColl:  "testcoll",
			MgoDocID: "doc-2",
			OpType:   events.OperationInsert,
		},
	}

	result := CoalesceEvents(evts)
	// doc-1 insert + delete = cancel out
	// doc-2 insert = kept
	if len(result) != 1 {
		t.Errorf("CoalesceEvents() returned %d events, want 1", len(result))
	}
}

func TestCoalesceEvents_Empty(t *testing.T) {
	t.Parallel()
	result := CoalesceEvents(nil)
	if result != nil {
		t.Errorf("CoalesceEvents(nil) = %v, want nil", result)
	}
}

func TestCoalescer_InsertThenReplace(t *testing.T) {
	t.Parallel()
	c := NewCoalescer()

	insert := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
		FullDocument: &storage.Document{
			Data: map[string]any{"name": "original"},
		},
	}
	c.Add(insert)

	replace := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationReplace,
		FullDocument: &storage.Document{
			Data: map[string]any{"name": "replaced"},
		},
	}
	c.Add(replace)

	// Should keep as insert with replaced data
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}

	if flushed[0].OpType != events.OperationInsert {
		t.Errorf("OpType = %s, want insert", flushed[0].OpType)
	}
	if flushed[0].FullDocument.Data["name"] != "replaced" {
		t.Errorf("FullDocument.Data['name'] = %v, want 'replaced'", flushed[0].FullDocument.Data["name"])
	}
}

func TestCoalescer_InsertThenInsert(t *testing.T) {
	c := NewCoalescer()

	insert1 := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}
	c.Add(insert1)

	insert2 := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}
	c.Add(insert2)

	// Should keep latest
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}
	if flushed[0].EventID != "evt-2" {
		t.Errorf("EventID = %s, want evt-2", flushed[0].EventID)
	}
}

func TestCoalescer_DeleteThenInsert(t *testing.T) {
	c := NewCoalescer()

	delete := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationDelete,
	}
	c.Add(delete)

	insert := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationInsert,
	}
	c.Add(insert)

	// Insert should win over delete (the insert is after delete)
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}
	if flushed[0].OpType != events.OperationInsert {
		t.Errorf("OpType = %s, want insert", flushed[0].OpType)
	}
}

func TestCoalescer_ReplaceThenUpdate(t *testing.T) {
	c := NewCoalescer()

	replace := &events.ChangeEvent{
		EventID:      "evt-1",
		MgoColl:      "testcoll",
		MgoDocID:     "doc-1",
		OpType:       events.OperationReplace,
		FullDocument: &storage.Document{Data: map[string]any{"name": "replaced"}},
	}
	c.Add(replace)

	update := &events.ChangeEvent{
		EventID:      "evt-2",
		MgoColl:      "testcoll",
		MgoDocID:     "doc-1",
		OpType:       events.OperationUpdate,
		FullDocument: &storage.Document{Data: map[string]any{"name": "updated"}},
	}
	c.Add(update)

	// Should keep latest update
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}
	if flushed[0].EventID != "evt-2" {
		t.Errorf("EventID = %s, want evt-2", flushed[0].EventID)
	}
}

func TestCoalescer_ReplaceThenDelete(t *testing.T) {
	c := NewCoalescer()

	replace := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationReplace,
	}
	c.Add(replace)

	delete := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationDelete,
	}
	c.Add(delete)

	// Should keep delete
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}
	if flushed[0].OpType != events.OperationDelete {
		t.Errorf("OpType = %s, want delete", flushed[0].OpType)
	}
}

func TestCoalescer_ReplaceThenReplace(t *testing.T) {
	c := NewCoalescer()

	replace1 := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationReplace,
	}
	c.Add(replace1)

	replace2 := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationReplace,
	}
	c.Add(replace2)

	// Should keep latest replace
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}
	if flushed[0].EventID != "evt-2" {
		t.Errorf("EventID = %s, want evt-2", flushed[0].EventID)
	}
}

func TestCoalescer_UpdateThenReplace(t *testing.T) {
	c := NewCoalescer()

	update := &events.ChangeEvent{
		EventID:  "evt-1",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationUpdate,
	}
	c.Add(update)

	replace := &events.ChangeEvent{
		EventID:  "evt-2",
		MgoColl:  "testcoll",
		MgoDocID: "doc-1",
		OpType:   events.OperationReplace,
	}
	c.Add(replace)

	// Should keep latest replace
	flushed := c.Flush()
	if len(flushed) != 1 {
		t.Fatalf("Flush() returned %d events, want 1", len(flushed))
	}
	if flushed[0].EventID != "evt-2" {
		t.Errorf("EventID = %s, want evt-2", flushed[0].EventID)
	}
}
