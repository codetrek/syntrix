package events

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestOperationType_IsValid(t *testing.T) {
	tests := []struct {
		op    OperationType
		valid bool
	}{
		{OperationInsert, true},
		{OperationUpdate, true},
		{OperationReplace, true},
		{OperationDelete, true},
		{"INSERT", false}, // uppercase is invalid
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			if got := tt.op.IsValid(); got != tt.valid {
				t.Errorf("OperationType(%q).IsValid() = %v, want %v", tt.op, got, tt.valid)
			}
		})
	}
}

func TestClusterTime_Compare(t *testing.T) {
	tests := []struct {
		name string
		a, b ClusterTime
		want int
	}{
		{"equal", ClusterTime{100, 1}, ClusterTime{100, 1}, 0},
		{"a.T < b.T", ClusterTime{99, 5}, ClusterTime{100, 1}, -1},
		{"a.T > b.T", ClusterTime{101, 1}, ClusterTime{100, 5}, 1},
		{"same T, a.I < b.I", ClusterTime{100, 1}, ClusterTime{100, 2}, -1},
		{"same T, a.I > b.I", ClusterTime{100, 3}, ClusterTime{100, 2}, 1},
		{"zero vs non-zero", ClusterTime{0, 0}, ClusterTime{1, 0}, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Compare(tt.b); got != tt.want {
				t.Errorf("ClusterTime.Compare() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterTime_IsZero(t *testing.T) {
	tests := []struct {
		ct   ClusterTime
		zero bool
	}{
		{ClusterTime{0, 0}, true},
		{ClusterTime{1, 0}, false},
		{ClusterTime{0, 1}, false},
		{ClusterTime{100, 5}, false},
	}

	for _, tt := range tests {
		if got := tt.ct.IsZero(); got != tt.zero {
			t.Errorf("ClusterTime{%d,%d}.IsZero() = %v, want %v", tt.ct.T, tt.ct.I, got, tt.zero)
		}
	}
}

func TestClusterTime_PrimitiveConversion(t *testing.T) {
	orig := primitive.Timestamp{T: 1234567890, I: 42}
	ct := ClusterTimeFromPrimitive(orig)

	if ct.T != orig.T || ct.I != orig.I {
		t.Errorf("ClusterTimeFromPrimitive() = {%d,%d}, want {%d,%d}", ct.T, ct.I, orig.T, orig.I)
	}

	back := ct.ToPrimitive()
	if back.T != orig.T || back.I != orig.I {
		t.Errorf("ToPrimitive() = {%d,%d}, want {%d,%d}", back.T, back.I, orig.T, orig.I)
	}
}

func TestNormalizedEvent_JSON(t *testing.T) {
	evt := &NormalizedEvent{
		EventID:     "evt-123",
		TenantID:    "tenant-abc",
		Collection:  "users",
		DocumentID:  "doc-456",
		Type:        OperationInsert,
		ClusterTime: ClusterTime{T: 1735567890, I: 1},
		Timestamp:   1735567890000,
		FullDocument: map[string]any{
			"name": "test",
			"age":  30,
		},
	}

	// Marshal
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Check JSON field names (should use short names)
	jsonStr := string(data)
	if !contains(jsonStr, `"tenant"`) {
		t.Errorf("JSON should contain 'tenant', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"documentId"`) {
		t.Errorf("JSON should contain 'documentId', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"operationType"`) {
		t.Errorf("JSON should contain 'operationType', got: %s", jsonStr)
	}

	// Unmarshal
	var decoded NormalizedEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.EventID != evt.EventID {
		t.Errorf("EventID = %q, want %q", decoded.EventID, evt.EventID)
	}
	if decoded.TenantID != evt.TenantID {
		t.Errorf("TenantID = %q, want %q", decoded.TenantID, evt.TenantID)
	}
	if decoded.Type != evt.Type {
		t.Errorf("Type = %q, want %q", decoded.Type, evt.Type)
	}
}

func TestNormalizedEvent_WithUpdateDescription(t *testing.T) {
	evt := &NormalizedEvent{
		EventID:     "evt-update",
		TenantID:    "tenant-1",
		Collection:  "docs",
		DocumentID:  "doc-1",
		Type:        OperationUpdate,
		ClusterTime: ClusterTime{T: 1735567890, I: 2},
		Timestamp:   1735567890000,
	}

	updateDesc := &UpdateDescription{
		UpdatedFields: map[string]any{"status": "active"},
		RemovedFields: []string{"oldField"},
	}

	evt.WithUpdateDescription(updateDesc)

	if evt.UpdateDesc == nil {
		t.Fatal("UpdateDesc should not be nil")
	}
	if evt.UpdateDesc.UpdatedFields["status"] != "active" {
		t.Errorf("UpdatedFields[status] = %v, want 'active'", evt.UpdateDesc.UpdatedFields["status"])
	}
	if len(evt.UpdateDesc.RemovedFields) != 1 || evt.UpdateDesc.RemovedFields[0] != "oldField" {
		t.Errorf("RemovedFields = %v, want [oldField]", evt.UpdateDesc.RemovedFields)
	}

	// Test JSON serialization with update description
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !contains(string(data), `"updateDescription"`) {
		t.Errorf("JSON should contain 'updateDescription'")
	}
}

func TestBufferKey(t *testing.T) {
	tests := []struct {
		name    string
		ct      ClusterTime
		eventID string
		want    string
	}{
		{
			name:    "normal",
			ct:      ClusterTime{T: 1735567890, I: 1},
			eventID: "evt-123",
			want:    "1735567890-0000000001-evt-123",
		},
		{
			name:    "zero increment",
			ct:      ClusterTime{T: 1735567890, I: 0},
			eventID: "evt-456",
			want:    "1735567890-0000000000-evt-456",
		},
		{
			name:    "large increment",
			ct:      ClusterTime{T: 1735567890, I: 999999999},
			eventID: "evt-789",
			want:    "1735567890-0999999999-evt-789",
		},
		{
			name:    "small T",
			ct:      ClusterTime{T: 1, I: 1},
			eventID: "a",
			want:    "0000000001-0000000001-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &NormalizedEvent{
				EventID:     tt.eventID,
				ClusterTime: tt.ct,
			}
			got := evt.BufferKey()
			if got != tt.want {
				t.Errorf("BufferKey() = %q, want %q", got, tt.want)
			}

			// Also test FormatBufferKey directly
			got2 := FormatBufferKey(tt.ct, tt.eventID)
			if got2 != tt.want {
				t.Errorf("FormatBufferKey() = %q, want %q", got2, tt.want)
			}
		})
	}
}

func TestBufferKey_Ordering(t *testing.T) {
	// Ensure lexicographic ordering matches temporal ordering
	events := []struct {
		ct      ClusterTime
		eventID string
	}{
		{ClusterTime{100, 1}, "a"},
		{ClusterTime{100, 2}, "b"},
		{ClusterTime{101, 0}, "c"},
		{ClusterTime{1000, 1}, "d"},
	}

	var keys []string
	for _, e := range events {
		keys = append(keys, FormatBufferKey(e.ct, e.eventID))
	}

	// Verify keys are in ascending order
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Errorf("Keys not in order: %q >= %q", keys[i-1], keys[i])
		}
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

func TestNewNormalizedEvent(t *testing.T) {
	ct := ClusterTime{T: 1735567890, I: 1}
	evt := NewNormalizedEvent(
		"evt-123",
		"tenant-1",
		"users",
		"doc-456",
		OperationInsert,
		ct,
	)

	if evt == nil {
		t.Fatal("NewNormalizedEvent() returned nil")
	}
	if evt.EventID != "evt-123" {
		t.Errorf("EventID = %q, want 'evt-123'", evt.EventID)
	}
	if evt.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q, want 'tenant-1'", evt.TenantID)
	}
	if evt.Collection != "users" {
		t.Errorf("Collection = %q, want 'users'", evt.Collection)
	}
	if evt.DocumentID != "doc-456" {
		t.Errorf("DocumentID = %q, want 'doc-456'", evt.DocumentID)
	}
	if evt.Type != OperationInsert {
		t.Errorf("Type = %q, want 'insert'", evt.Type)
	}
	if evt.ClusterTime != ct {
		t.Errorf("ClusterTime = %+v, want %+v", evt.ClusterTime, ct)
	}
	if evt.Timestamp == 0 {
		t.Error("Timestamp should be set to current time")
	}
}

func TestNormalizedEvent_WithFullDocument(t *testing.T) {
	evt := &NormalizedEvent{
		EventID: "evt-123",
	}

	doc := map[string]any{"name": "test", "value": 123}
	result := evt.WithFullDocument(doc)

	// Should return same event for chaining
	if result != evt {
		t.Error("WithFullDocument should return same event")
	}
	if evt.FullDocument == nil {
		t.Error("FullDocument should be set")
	}
	if evt.FullDocument["name"] != "test" {
		t.Errorf("FullDocument[name] = %v, want 'test'", evt.FullDocument["name"])
	}
}

func TestNormalizedEvent_WithTxnNumber(t *testing.T) {
	evt := &NormalizedEvent{
		EventID: "evt-123",
	}

	result := evt.WithTxnNumber(42)

	// Should return same event for chaining
	if result != evt {
		t.Error("WithTxnNumber should return same event")
	}
	if evt.TxnNumber == nil {
		t.Error("TxnNumber should be set")
	}
	if *evt.TxnNumber != 42 {
		t.Errorf("TxnNumber = %v, want 42", *evt.TxnNumber)
	}
}

func TestNormalizedEvent_Chaining(t *testing.T) {
	// Test method chaining works
	evt := NewNormalizedEvent("evt-1", "tenant", "coll", "doc", OperationUpdate, ClusterTime{100, 1}).
		WithFullDocument(map[string]any{"updated": true}).
		WithUpdateDescription(&UpdateDescription{
			UpdatedFields: map[string]any{"field": "value"},
		}).
		WithTxnNumber(123)

	if evt.EventID != "evt-1" {
		t.Error("EventID should be preserved")
	}
	if evt.FullDocument == nil {
		t.Error("FullDocument should be set")
	}
	if evt.UpdateDesc == nil {
		t.Error("UpdateDesc should be set")
	}
	if evt.TxnNumber == nil || *evt.TxnNumber != 123 {
		t.Error("TxnNumber should be 123")
	}
}
