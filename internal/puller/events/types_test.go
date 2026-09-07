package events

import (
	"encoding/json"
	"testing"

	"github.com/syntrixbase/syntrix/internal/core/storage"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestOperationType_IsValid(t *testing.T) {
	tests := []struct {
		op    StoreOperationType
		valid bool
	}{
		{StoreOperationInsert, true},
		{StoreOperationUpdate, true},
		{StoreOperationReplace, true},
		{StoreOperationDelete, true},
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

func TestChangeEvent_JSON(t *testing.T) {
	evt := &StoreChangeEvent{
		EventID:     "evt-123",
		Database:    "database-abc",
		MgoColl:     "users",
		MgoDocID:    "doc-456",
		OpType:      StoreOperationInsert,
		ClusterTime: ClusterTime{T: 1735567890, I: 1},
		Timestamp:   1735567890000,
		FullDocument: &storage.StoredDoc{
			Id:       "doc-456",
			Database: "database-abc",
		},
	}

	// Marshal
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	// Check JSON field names (should use short names)
	jsonStr := string(data)
	if !contains(jsonStr, `"database"`) {
		t.Errorf("JSON should contain 'database', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"mgoDocId"`) {
		t.Errorf("JSON should contain 'mgoDocId', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"mgoColl"`) {
		t.Errorf("JSON should contain 'mgoColl', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"opType"`) {
		t.Errorf("JSON should contain 'opType', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"fullDoc"`) {
		t.Errorf("JSON should contain 'fullDoc', got: %s", jsonStr)
	}

	// Unmarshal
	var decoded StoreChangeEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.EventID != evt.EventID {
		t.Errorf("EventID = %q, want %q", decoded.EventID, evt.EventID)
	}
	if decoded.Database != evt.Database {
		t.Errorf("Database = %q, want %q", decoded.Database, evt.Database)
	}
	if decoded.OpType != evt.OpType {
		t.Errorf("OpType = %q, want %q", decoded.OpType, evt.OpType)
	}
	if decoded.FullDocument.Id != evt.FullDocument.Id {
		t.Errorf("FullDocument.Id = %q, want %q", decoded.FullDocument.Id, evt.FullDocument.Id)
	}
}

func TestPullerEvent_JSON(t *testing.T) {
	change := &StoreChangeEvent{
		EventID: "evt-123",
		OpType:  StoreOperationInsert,
	}
	evt := &PullerEvent{
		Change:   change,
		Progress: "resume-token-123",
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	jsonStr := string(data)
	if !contains(jsonStr, `"change_event"`) {
		t.Errorf("JSON should contain 'change_event', got: %s", jsonStr)
	}
	if !contains(jsonStr, `"progress"`) {
		t.Errorf("JSON should contain 'progress', got: %s", jsonStr)
	}

	var decoded PullerEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if decoded.Progress != evt.Progress {
		t.Errorf("Progress = %q, want %q", decoded.Progress, evt.Progress)
	}
	if decoded.Change.EventID != evt.Change.EventID {
		t.Errorf("Change.EventID = %q, want %q", decoded.Change.EventID, evt.Change.EventID)
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
