package client

import (
	"testing"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/puller/events"
)

func TestConvertEvent(t *testing.T) {
	t.Parallel()
	c := &Client{}

	tests := []struct {
		name     string
		input    *pullerv1.Event
		expected *events.NormalizedEvent
	}{
		{
			name: "basic event",
			input: &pullerv1.Event{
				Id:            "evt-123",
				Tenant:        "tenant-1",
				Collection:    "users",
				DocumentId:    "doc-1",
				OperationType: "insert",
				Timestamp:     1234567890,
			},
			expected: &events.NormalizedEvent{
				EventID:    "evt-123",
				TenantID:   "tenant-1",
				Collection: "users",
				DocumentID: "doc-1",
				Type:       events.OperationType("insert"),
				Timestamp:  1234567890,
			},
		},
		{
			name: "event with cluster time",
			input: &pullerv1.Event{
				Id:            "evt-456",
				Tenant:        "tenant-2",
				Collection:    "orders",
				DocumentId:    "doc-2",
				OperationType: "update",
				ClusterTime: &pullerv1.ClusterTime{
					T: 100,
					I: 5,
				},
			},
			expected: &events.NormalizedEvent{
				EventID:    "evt-456",
				TenantID:   "tenant-2",
				Collection: "orders",
				DocumentID: "doc-2",
				Type:       events.OperationType("update"),
				ClusterTime: events.ClusterTime{
					T: 100,
					I: 5,
				},
			},
		},
		{
			name: "event without cluster time",
			input: &pullerv1.Event{
				Id:            "evt-789",
				OperationType: "delete",
			},
			expected: &events.NormalizedEvent{
				EventID: "evt-789",
				Type:    events.OperationType("delete"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.convertEvent(tt.input)

			if result.EventID != tt.expected.EventID {
				t.Errorf("EventID = %v, want %v", result.EventID, tt.expected.EventID)
			}
			if result.TenantID != tt.expected.TenantID {
				t.Errorf("TenantID = %v, want %v", result.TenantID, tt.expected.TenantID)
			}
			if result.Collection != tt.expected.Collection {
				t.Errorf("Collection = %v, want %v", result.Collection, tt.expected.Collection)
			}
			if result.DocumentID != tt.expected.DocumentID {
				t.Errorf("DocumentID = %v, want %v", result.DocumentID, tt.expected.DocumentID)
			}
			if result.Type != tt.expected.Type {
				t.Errorf("Type = %v, want %v", result.Type, tt.expected.Type)
			}
			if result.Timestamp != tt.expected.Timestamp {
				t.Errorf("Timestamp = %v, want %v", result.Timestamp, tt.expected.Timestamp)
			}
			if result.ClusterTime.T != tt.expected.ClusterTime.T {
				t.Errorf("ClusterTime.T = %v, want %v", result.ClusterTime.T, tt.expected.ClusterTime.T)
			}
			if result.ClusterTime.I != tt.expected.ClusterTime.I {
				t.Errorf("ClusterTime.I = %v, want %v", result.ClusterTime.I, tt.expected.ClusterTime.I)
			}
		})
	}
}

func TestClient_Close_NilConn(t *testing.T) {
	t.Parallel()
	c := &Client{
		conn: nil,
	}

	err := c.Close()
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}
