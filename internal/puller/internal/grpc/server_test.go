package grpc

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/events"
)

// mockEventSource implements the EventSource interface for testing
type mockEventSource struct {
	handler func(ctx context.Context, backendName string, event *events.NormalizedEvent) error
}

func (m *mockEventSource) SetEventHandler(handler func(ctx context.Context, backendName string, event *events.NormalizedEvent) error) {
	m.handler = handler
}

func TestNewServer(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{
		Address:        ":50051",
		MaxConnections: 100,
	}

	source := &mockEventSource{}

	// Test with nil logger
	server := NewServer(cfg, source, nil)
	if server == nil {
		t.Fatal("NewServer() returned nil")
	}
	if server.subs == nil {
		t.Error("subscriber manager should be initialized")
	}
	if server.eventChan == nil {
		t.Error("event channel should be initialized")
	}

	// Test with provided logger
	logger := slog.Default()
	server2 := NewServer(cfg, source, logger)
	if server2 == nil {
		t.Fatal("NewServer() with logger returned nil")
	}
}

func TestServer_SubscriberCount(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{}
	source := &mockEventSource{}
	server := NewServer(cfg, source, nil)

	if server.SubscriberCount() != 0 {
		t.Errorf("SubscriberCount() = %d, want 0", server.SubscriberCount())
	}

	// Add a subscriber
	sub := NewSubscriber("consumer-1", nil, false)
	server.subs.Add(sub)

	if server.SubscriberCount() != 1 {
		t.Errorf("SubscriberCount() = %d, want 1", server.SubscriberCount())
	}
}

func TestServer_ConvertEvent(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{}
	source := &mockEventSource{}
	server := NewServer(cfg, source, nil)

	tests := []struct {
		name    string
		backend string
		event   *events.NormalizedEvent
		wantErr bool
	}{
		{
			name:    "basic event",
			backend: "backend-1",
			event: &events.NormalizedEvent{
				EventID:    "evt-123",
				TenantID:   "tenant-1",
				Collection: "users",
				DocumentID: "doc-1",
				Type:       events.OperationInsert,
				ClusterTime: events.ClusterTime{
					T: 100,
					I: 1,
				},
				Timestamp: 1234567890,
			},
			wantErr: false,
		},
		{
			name:    "event with full document",
			backend: "backend-2",
			event: &events.NormalizedEvent{
				EventID:      "evt-456",
				Type:         events.OperationUpdate,
				FullDocument: map[string]any{"name": "test", "value": 123},
			},
			wantErr: false,
		},
		{
			name:    "event with update description",
			backend: "backend-3",
			event: &events.NormalizedEvent{
				EventID: "evt-789",
				Type:    events.OperationUpdate,
				UpdateDesc: &events.UpdateDescription{
					UpdatedFields: map[string]any{"name": "new"},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := server.convertEvent(tt.backend, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertEvent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}

			if result.Id != tt.event.EventID {
				t.Errorf("Id = %v, want %v", result.Id, tt.event.EventID)
			}
			if result.Tenant != tt.event.TenantID {
				t.Errorf("Tenant = %v, want %v", result.Tenant, tt.event.TenantID)
			}
			if result.Collection != tt.event.Collection {
				t.Errorf("Collection = %v, want %v", result.Collection, tt.event.Collection)
			}
			if result.DocumentId != tt.event.DocumentID {
				t.Errorf("DocumentId = %v, want %v", result.DocumentId, tt.event.DocumentID)
			}
			if result.OperationType != string(tt.event.Type) {
				t.Errorf("OperationType = %v, want %v", result.OperationType, string(tt.event.Type))
			}
			if result.Timestamp != tt.event.Timestamp {
				t.Errorf("Timestamp = %v, want %v", result.Timestamp, tt.event.Timestamp)
			}
			if result.ClusterTime.T != tt.event.ClusterTime.T {
				t.Errorf("ClusterTime.T = %v, want %v", result.ClusterTime.T, tt.event.ClusterTime.T)
			}
			if result.ClusterTime.I != tt.event.ClusterTime.I {
				t.Errorf("ClusterTime.I = %v, want %v", result.ClusterTime.I, tt.event.ClusterTime.I)
			}
		})
	}
}

func TestServer_Stop_NotRunning(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{}
	source := &mockEventSource{}
	server := NewServer(cfg, source, nil)

	ctx := context.Background()
	err := server.Stop(ctx)
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
}

func TestServer_Stop_Running(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{}
	source := &mockEventSource{}
	server := NewServer(cfg, source, nil)

	// Simulate running state
	server.running = true

	// Add some subscribers
	sub1 := NewSubscriber("consumer-1", nil, false)
	sub2 := NewSubscriber("consumer-2", nil, false)
	server.subs.Add(sub1)
	server.subs.Add(sub2)

	ctx := context.Background()
	err := server.Stop(ctx)
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}

	// Verify running is now false
	if server.running {
		t.Error("running should be false after Stop()")
	}

	// Verify subscribers were closed
	if server.subs.Count() != 0 {
		t.Errorf("subscriber count = %d, want 0", server.subs.Count())
	}
}

func TestServer_Stop_WithTimeout(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{}
	source := &mockEventSource{}
	server := NewServer(cfg, source, nil)

	// Simulate running state but no grpc server (nil)
	server.running = true
	server.grpcServer = nil

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := server.Stop(ctx)
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}

	if server.running {
		t.Error("running should be false after Stop()")
	}
}

func TestProgressMarker_SetPosition_NilPositions(t *testing.T) {
	t.Parallel()
	pm := &ProgressMarker{Positions: nil}
	pm.SetPosition("backend-1", "evt-123")

	if pm.Positions == nil {
		t.Error("Positions should be initialized")
	}
	if pm.Positions["backend-1"] != "evt-123" {
		t.Error("Position should be set")
	}
}

func TestProgressMarker_GetPosition_NilPositions(t *testing.T) {
	t.Parallel()
	pm := &ProgressMarker{Positions: nil}
	pos := pm.GetPosition("backend-1")

	if pos != "" {
		t.Errorf("GetPosition() = %q, want empty string", pos)
	}
}

func TestProgressMarker_Clone_Nil(t *testing.T) {
	t.Parallel()
	var pm *ProgressMarker
	clone := pm.Clone()

	if clone == nil {
		t.Error("Clone() of nil should return empty marker")
	}
	if clone.Positions == nil {
		t.Error("Cloned marker should have initialized Positions")
	}
}
