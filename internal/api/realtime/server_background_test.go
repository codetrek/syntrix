package realtime

import (
	"context"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
)

type mockQueryWatchError struct{}

func (m *mockQueryWatchError) GetDocument(ctx context.Context, path string) (model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchError) CreateDocument(ctx context.Context, doc model.Document) error {
	return nil
}
func (m *mockQueryWatchError) ReplaceDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchError) PatchDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchError) DeleteDocument(ctx context.Context, path string, pred model.Filters) error {
	return nil
}
func (m *mockQueryWatchError) ExecuteQuery(ctx context.Context, q model.Query) ([]model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchError) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	return nil, assert.AnError
}
func (m *mockQueryWatchError) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}
func (m *mockQueryWatchError) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}

type mockQueryWatchStream struct {
	stream chan storage.Event
}

func (m *mockQueryWatchStream) GetDocument(ctx context.Context, path string) (model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchStream) CreateDocument(ctx context.Context, doc model.Document) error {
	return nil
}
func (m *mockQueryWatchStream) ReplaceDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchStream) PatchDocument(ctx context.Context, data model.Document, pred model.Filters) (model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchStream) DeleteDocument(ctx context.Context, path string, pred model.Filters) error {
	return nil
}
func (m *mockQueryWatchStream) ExecuteQuery(ctx context.Context, q model.Query) ([]model.Document, error) {
	return nil, nil
}
func (m *mockQueryWatchStream) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	return m.stream, nil
}
func (m *mockQueryWatchStream) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}
func (m *mockQueryWatchStream) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}

func TestServer_StartBackgroundTasks_WatchError(t *testing.T) {
	srv := NewServer(&mockQueryWatchError{}, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.StartBackgroundTasks(ctx)
	assert.Error(t, err)
}

func TestServer_StartBackgroundTasks_Broadcast(t *testing.T) {
	stream := make(chan storage.Event, 1)
	qs := &mockQueryWatchStream{stream: stream}
	srv := NewServer(qs, "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.StartBackgroundTasks(ctx)
	assert.NoError(t, err)

	// Register a client with a subscription to capture broadcast
	client := &Client{
		hub:           srv.hub,
		queryService:  qs,
		send:          make(chan BaseMessage, 1),
		subscriptions: map[string]Subscription{"sub": {Query: model.Query{}, IncludeData: true}},
	}
	srv.hub.Register(client)

	// Emit an event and close stream to stop watcher
	stream <- storage.Event{Id: "users/1", Type: storage.EventCreate, Document: &storage.Document{Fullpath: "users/1", Collection: "users", Data: map[string]interface{}{"foo": "bar"}}}
	close(stream)

	select {
	case msg := <-client.send:
		assert.Equal(t, TypeEvent, msg.Type)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected broadcast message")
	}
}
