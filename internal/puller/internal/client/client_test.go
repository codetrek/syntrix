package client

import (
	"log/slog"
	"testing"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/codetrek/syntrix/internal/storage"
)

func TestConvertEvent(t *testing.T) {
	t.Parallel()
	c := &Client{
		logger: slog.Default(),
	}

	tests := []struct {
		name     string
		input    *pullerv1.PullerEvent
		expected *events.PullerEvent
	}{
		{
			name: "basic event",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId:   "evt-123",
					Tenant:    "tenant-1",
					MgoColl:   "users",
					MgoDocId:  "doc-1",
					OpType:    "insert",
					Timestamp: 1234567890,
				},
				Progress: "p1",
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID:   "evt-123",
					TenantID:  "tenant-1",
					MgoColl:   "users",
					MgoDocID:  "doc-1",
					OpType:    events.OperationType("insert"),
					Timestamp: 1234567890,
				},
				Progress: "p1",
			},
		},
		{
			name: "event with cluster time",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId:  "evt-456",
					Tenant:   "tenant-2",
					MgoColl:  "orders",
					MgoDocId: "doc-2",
					OpType:   "update",
					ClusterTime: &pullerv1.ClusterTime{
						T: 100,
						I: 5,
					},
				},
				Progress: "p2",
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID:  "evt-456",
					TenantID: "tenant-2",
					MgoColl:  "orders",
					MgoDocID: "doc-2",
					OpType:   events.OperationType("update"),
					ClusterTime: events.ClusterTime{
						T: 100,
						I: 5,
					},
				},
				Progress: "p2",
			},
		},
		{
			name: "event without cluster time",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId: "evt-789",
					OpType:  "delete",
				},
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID: "evt-789",
					OpType:  events.OperationType("delete"),
				},
			},
		},
		{
			name: "event with full document",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId: "evt-full",
					FullDoc: []byte(`{"_id":"doc-1","data":{"name":"test"}}`),
				},
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID: "evt-full",
					FullDocument: &storage.Document{
						Id:   "doc-1",
						Data: map[string]interface{}{"name": "test"},
					},
				},
			},
		},
		{
			name: "event with update description",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId:    "evt-update",
					UpdateDesc: []byte(`{"updatedFields":{"name":"new"},"removedFields":["old"]}`),
				},
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID: "evt-update",
					UpdateDesc: &events.UpdateDescription{
						UpdatedFields: map[string]interface{}{"name": "new"},
						RemovedFields: []string{"old"},
					},
				},
			},
		},
		{
			name: "event with invalid full document",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId: "evt-invalid-doc",
					FullDoc: []byte(`{invalid-json}`),
				},
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID: "evt-invalid-doc",
				},
			},
		},
		{
			name: "event with invalid update description",
			input: &pullerv1.PullerEvent{
				ChangeEvent: &pullerv1.ChangeEvent{
					EventId:    "evt-invalid-desc",
					UpdateDesc: []byte(`{invalid-json}`),
				},
			},
			expected: &events.PullerEvent{
				Change: &events.ChangeEvent{
					EventID: "evt-invalid-desc",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.convertEvent(tt.input)

			if result.Progress != tt.expected.Progress {
				t.Errorf("Progress = %v, want %v", result.Progress, tt.expected.Progress)
			}

			if result.Change == nil && tt.expected.Change == nil {
				return
			}
			if result.Change == nil || tt.expected.Change == nil {
				t.Errorf("Change mismatch: got %v, want %v", result.Change, tt.expected.Change)
				return
			}

			change := result.Change
			expChange := tt.expected.Change

			if change.EventID != expChange.EventID {
				t.Errorf("EventID = %v, want %v", change.EventID, expChange.EventID)
			}
			if change.TenantID != expChange.TenantID {
				t.Errorf("TenantID = %v, want %v", change.TenantID, expChange.TenantID)
			}
			if change.MgoColl != expChange.MgoColl {
				t.Errorf("MgoColl = %v, want %v", change.MgoColl, expChange.MgoColl)
			}
			if change.MgoDocID != expChange.MgoDocID {
				t.Errorf("MgoDocID = %v, want %v", change.MgoDocID, expChange.MgoDocID)
			}
			if change.OpType != expChange.OpType {
				t.Errorf("OpType = %v, want %v", change.OpType, expChange.OpType)
			}
			if change.Timestamp != expChange.Timestamp {
				t.Errorf("Timestamp = %v, want %v", change.Timestamp, expChange.Timestamp)
			}
			if change.ClusterTime.T != expChange.ClusterTime.T {
				t.Errorf("ClusterTime.T = %v, want %v", change.ClusterTime.T, expChange.ClusterTime.T)
			}
		})
	}
}
