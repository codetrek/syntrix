package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/syntrixbase/syntrix/api/gen/streamer/v1"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/pkg/model"
)

var testLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

func init() {
	slog.SetDefault(testLogger)
}

// testStoredDoc creates a StoredDoc with all required fields for testing.
// Note: StoredDoc.Id is MongoDB's _id (database:hash format), NOT the user document ID.
// The user document ID comes from Fullpath (collection/docID) and should also be in Data["id"].
func testStoredDoc(collection, docID, database string, data map[string]interface{}) *storage.StoredDoc {
	// Ensure data has the user document ID
	if data == nil {
		data = make(map[string]interface{})
	}
	data["id"] = docID

	return &storage.StoredDoc{
		Id:         "_id_" + docID, // MongoDB _id (not the user document ID)
		Database:   database,
		Collection: collection,
		Fullpath:   collection + "/" + docID, // User document ID is extracted from here
		Data:       data,
	}
}

// testSyntrixEvent creates a SyntrixChangeEvent for testing.
// This is the business-layer event that ProcessEvent expects.
func testSyntrixEvent(eventID, database, collection, docID string, eventType events.EventType, data map[string]interface{}) events.SyntrixChangeEvent {
	return events.SyntrixChangeEvent{
		Id:        eventID,
		Database:  database,
		Type:      eventType,
		Document:  testStoredDoc(collection, docID, database, data),
		Timestamp: time.Now().UnixMilli(),
	}
}

func TestNewService(t *testing.T) {
	s, err := NewService(ServerConfig{}, nil)
	require.NoError(t, err)
	require.NotNil(t, s)
}

func getInternalService(s StreamerServer) *streamerService {
	return s.(*streamerService)
}

func TestService_Stream(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stream)

	err = stream.Close()
	require.NoError(t, err)
}

func TestService_Stream_Subscribe(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	subID, err := stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, subID)
}

func TestService_Stream_SubscribeWithFilters(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	subID, err := stream.Subscribe("database1", "users", []model.Filter{
		{Field: "status", Op: model.OpEq, Value: "active"},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, subID)
}

func TestService_ProcessEvent_NoSubscriptions(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	err = internal.ProcessEvent(testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventCreate, nil))
	require.NoError(t, err)
}

func TestService_ProcessEvent_WithSubscription(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	subID, err := stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	err = internal.ProcessEvent(testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventCreate, map[string]interface{}{"name": "Alice"}))
	require.NoError(t, err)

	delivery, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, delivery)
	assert.Equal(t, []string{subID}, delivery.SubscriptionIDs)
	assert.Equal(t, "evt1", delivery.Event.EventID)
	assert.Equal(t, "database1", delivery.Event.Database)
	assert.Equal(t, "users", delivery.Event.Collection)
	assert.Equal(t, "doc1", delivery.Event.DocumentID)
	assert.Equal(t, OperationInsert, delivery.Event.Operation)
}

func TestService_ProcessEvent_MultipleStreams(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream1, _ := s.Stream(context.Background())
	stream2, _ := s.Stream(context.Background())
	defer stream1.Close()
	defer stream2.Close()

	subID1, _ := stream1.Subscribe("database1", "users", nil)
	subID2, _ := stream2.Subscribe("database1", "users", nil)

	internal.ProcessEvent(testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventCreate, map[string]interface{}{"name": "Alice"}))

	msg1, err := stream1.Recv()
	require.NoError(t, err)
	assert.Equal(t, []string{subID1}, msg1.SubscriptionIDs)

	msg2, err := stream2.Recv()
	require.NoError(t, err)
	assert.Equal(t, []string{subID2}, msg2.SubscriptionIDs)
}

func TestService_Stop(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, _ := s.Stream(context.Background())
	require.NotNil(t, stream)

	err = s.Stop(context.Background())
	require.NoError(t, err)
}

func TestService_SubscribeWithManager(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, _ := s.Stream(context.Background())
	defer stream.Close()

	_, err = stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	resp, err := internal.manager.Subscribe("test-gw", &pb.SubscribeRequest{
		SubscriptionId: "sub2",
		Database:       "database1",
		Collection:     "users",
	})
	require.NoError(t, err)
	require.True(t, resp.Success)
}

func TestService_Start(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	err = s.Start(context.Background())
	require.NoError(t, err)
}

func TestService_Stream_Unsubscribe(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	subID, err := stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	err = stream.Unsubscribe(subID)
	require.NoError(t, err)
}

func TestService_Stream_ContextCancel(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := s.Stream(ctx)
	require.NoError(t, err)

	cancel()
	time.Sleep(10 * time.Millisecond)

	_, err = stream.Subscribe("database1", "users", nil)
	require.Error(t, err)
}

func TestService_Recv_ContextCanceled(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := s.Stream(ctx)
	require.NoError(t, err)

	cancel()
	time.Sleep(10 * time.Millisecond)
	_, err = stream.Recv()
	require.Error(t, err)
}

func TestService_ProcessEventJSON(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	subID, err := stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	// ProcessEventJSON expects PullerEvent format (wrapper with change_event and progress)
	jsonData := []byte(`{
		"change_event": {
			"eventId": "evt1",
			"database": "database1",
			"mgoColl": "users",
			"mgoDocId": "doc1",
			"opType": "insert",
			"fullDoc": {
				"id": "_id_doc1",
				"database": "database1",
				"collection": "users",
				"fullpath": "users/doc1",
				"data": {"id": "doc1", "name": "Alice"}
			}
		},
		"progress": "progress-1"
	}`)

	err = internal.ProcessEventJSON(jsonData)
	require.NoError(t, err)

	delivery, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, delivery)
	assert.Contains(t, delivery.SubscriptionIDs, subID)
}

func TestService_ProcessEventJSON_InvalidJSON(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	err = internal.ProcessEventJSON([]byte("invalid json"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal event")
}

func TestService_ProcessEventJSON_TransformError(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	// Event with unknown operation type will cause Transform to return ErrUnknownOpType
	jsonData := []byte(`{
		"change_event": {
			"eventId": "evt1",
			"database": "database1",
			"mgoColl": "users",
			"mgoDocId": "doc1",
			"opType": "unknown_operation"
		},
		"progress": "progress-1"
	}`)

	err = internal.ProcessEventJSON(jsonData)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to transform event")
}

func TestService_ProcessEventJSON_DeleteIgnored(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	// Hard delete operation should be silently ignored
	jsonData := []byte(`{
		"change_event": {
			"eventId": "evt1",
			"database": "database1",
			"mgoColl": "users",
			"mgoDocId": "doc1",
			"opType": "delete"
		},
		"progress": "progress-1"
	}`)

	err = internal.ProcessEventJSON(jsonData)
	require.NoError(t, err) // Should not error, just silently ignore
}

// TestService_Stream_ClosedOperations tests that operations on a closed stream return errors.
func TestService_Stream_ClosedOperations(t *testing.T) {
	t.Parallel()

	t.Run("Subscribe", func(t *testing.T) {
		s, err := NewService(ServerConfig{}, slog.Default())
		require.NoError(t, err)

		stream, err := s.Stream(context.Background())
		require.NoError(t, err)

		err = stream.Close()
		require.NoError(t, err)

		_, err = stream.Subscribe("database1", "users", nil)
		require.Error(t, err)
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		s, err := NewService(ServerConfig{}, slog.Default())
		require.NoError(t, err)

		stream, err := s.Stream(context.Background())
		require.NoError(t, err)

		stream.Close()

		err = stream.Unsubscribe("sub1")
		require.Error(t, err)
	})

	t.Run("Recv", func(t *testing.T) {
		s, err := NewService(ServerConfig{}, slog.Default())
		require.NoError(t, err)

		stream, err := s.Stream(context.Background())
		require.NoError(t, err)

		stream.Close()
		time.Sleep(10 * time.Millisecond)

		_, err = stream.Recv()
		require.Error(t, err)
	})
}

func TestService_ProcessEvent_Timeout(t *testing.T) {
	s, err := NewService(ServerConfig{SendTimeout: 10 * time.Millisecond}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	_, err = stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	// Fill up the outgoing channel to cause timeout
	for i := 0; i < 1100; i++ {
		internal.ProcessEvent(testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventCreate, map[string]interface{}{"name": "Alice"}))
	}
	// Just ensure no panic, timeout behavior is timing-dependent
}

func TestService_Unsubscribe_NotFound(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	// Unsubscribe a non-existent subscription - now returns error (direct call to manager)
	err = stream.Unsubscribe("nonexistent-sub-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscription not found")
}

func TestService_Subscribe_Error(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	// Subscribe without filters - should succeed
	subID, err := stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, subID)
}

func TestService_MultipleSubscriptions(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	// Create multiple subscriptions
	subID1, err := stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	subID2, err := stream.Subscribe("database1", "orders", nil)
	require.NoError(t, err)

	assert.NotEqual(t, subID1, subID2)

	// Send an event that matches the first subscription
	err = internal.ProcessEvent(testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventCreate, map[string]interface{}{"name": "Alice"}))
	require.NoError(t, err)

	delivery, err := stream.Recv()
	require.NoError(t, err)
	assert.Contains(t, delivery.SubscriptionIDs, subID1)
}

func TestService_RecvNoMatch(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	// Subscribe to a different collection - should not match
	_, err = stream.Subscribe("database1", "orders", nil)
	require.NoError(t, err)

	// Send event to users collection (not orders)
	err = internal.ProcessEvent(testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventCreate, map[string]interface{}{"name": "Alice"}))
	require.NoError(t, err)

	// Should timeout since no match
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		stream.Recv()
		close(done)
	}()

	select {
	case <-ctx.Done():
		// Expected - timeout means no match
	case <-done:
		t.Error("Unexpected delivery")
	}
}

func TestService_Close_CancelsContext(t *testing.T) {
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)

	// Subscribe first
	_, err = stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	// Close should work
	err = stream.Close()
	require.NoError(t, err)

	// Subsequent operations should fail
	_, err = stream.Subscribe("database2", "orders", nil)
	require.Error(t, err)
}

func TestService_ProcessEvent_DeleteOperation(t *testing.T) {
	t.Parallel()
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	_, err = stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)

	// Delete event: SyntrixChangeEvent with EventDelete type
	// Note: Delete events without document are skipped by ProcessEvent (nil check)
	// So we test with a document that has Deleted=true
	deleteEvent := testSyntrixEvent("evt1", "database1", "users", "doc1", events.EventDelete, nil)
	deleteEvent.Document.Deleted = true
	err = internal.ProcessEvent(deleteEvent)
	require.NoError(t, err)

	// Event should be delivered
	delivery, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, OperationDelete, delivery.Event.Operation)
}

type testPullerService struct {
	events      chan *puller.Event
	requests    chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
	err         error
	terminalErr error
	closeErr    error
	options     puller.SubscribeOptions
}

func newTestPullerService() *testPullerService {
	return &testPullerService{
		events:      make(chan *puller.Event, 10),
		requests:    make(chan struct{}, 10),
		closed:      make(chan struct{}),
		terminalErr: io.EOF,
	}
}

func (m *testPullerService) Subscribe(ctx context.Context, opts puller.SubscribeOptions) (puller.Subscription, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.options = opts
	return m, nil
}

func (m *testPullerService) Next(ctx context.Context) (*puller.Event, error) {
	m.requests <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.closed:
		return nil, io.EOF
	case evt, ok := <-m.events:
		if !ok {
			return nil, m.terminalErr
		}
		return evt, nil
	}
}

func (m *testPullerService) Close() error {
	m.closeOnce.Do(func() { close(m.closed) })
	return m.closeErr
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for lifecycle signal")
	}
}

func TestStart_WithMockPuller(t *testing.T) {
	t.Parallel()
	upstream := newTestPullerService()
	s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Stop(context.Background())) })
	require.NoError(t, s.Start(context.Background()))
	assert.Equal(t, puller.SubscribeOptions{ConsumerID: "streamer"}, upstream.options)
	require.ErrorContains(t, s.Start(context.Background()), "already started")
}

func TestStart_SubscribeFailure(t *testing.T) {
	t.Parallel()
	upstream := newTestPullerService()
	upstream.err = errors.New("history expired")
	s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	require.ErrorIs(t, s.Start(context.Background()), upstream.err)
	require.ErrorIs(t, s.Start(context.Background()), upstream.err)
	waitSignal(t, getInternalService(s).Done())
	_, err = stream.Recv()
	require.ErrorIs(t, err, upstream.err)
	_, err = s.Stream(context.Background())
	require.ErrorIs(t, err, upstream.err)
}

func TestStop_PreservesSubscriptionCloseError(t *testing.T) {
	upstream := newTestPullerService()
	upstream.closeErr = errors.New("close subscription failed")
	s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.ErrorIs(t, s.Stop(ctx), upstream.closeErr)
	require.ErrorIs(t, s.Stop(ctx), upstream.closeErr)
}

func TestStart_InvalidPullerAddressPreservesError(t *testing.T) {
	s, err := NewService(ServerConfig{PullerAddr: "invalid%zz"}, slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = s.Start(ctx)
	require.ErrorContains(t, err, "create puller client")
	var parseError *url.Error
	require.ErrorAs(t, err, &parseError)
	assert.Nil(t, getInternalService(s).pullerClient)
	assert.False(t, getInternalService(s).started)
	require.NoError(t, s.Stop(ctx))
}

func TestStart_OwnsRemotePullerClient(t *testing.T) {
	address := "unix://" + filepath.Join(t.TempDir(), "missing-puller.sock")
	s, err := NewService(ServerConfig{PullerAddr: address}, slog.Default())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, s.Start(ctx))
	client := getInternalService(s).pullerClient
	require.NotNil(t, client)
	require.NoError(t, s.Stop(ctx))
	subscription, err := client.Subscribe(context.Background(), puller.SubscribeOptions{ConsumerID: "after-stop"})
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, subscription)
}

func TestStart_StandaloneMode(t *testing.T) {
	t.Parallel()
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	require.NoError(t, s.Stop(context.Background()))
}

func TestConsumePullerEvents_CheckpointAfterDelivery(t *testing.T) {
	t.Parallel()
	upstream := newTestPullerService()
	s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Stop(context.Background())) })
	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	_, err = stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	waitSignal(t, upstream.requests)
	upstream.events <- &puller.Event{
		Change: &events.StoreChangeEvent{
			EventID:      "event-1",
			Database:     "database1",
			OpType:       events.StoreOperationInsert,
			FullDocument: testStoredDoc("users", "doc1", "database1", nil),
		},
		Progress: "processed-1",
	}
	waitSignal(t, upstream.requests)
	assert.Equal(t, "processed-1", getInternalService(s).progress)
	delivery, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "event-1", delivery.Event.EventID)

	upstream.events <- &puller.Event{Progress: "processed-2"}
	waitSignal(t, upstream.requests)
	assert.Equal(t, "processed-2", getInternalService(s).progress)
}

func TestConsumePullerEvents_TransformFailureKeepsCheckpoint(t *testing.T) {
	t.Parallel()
	upstream := newTestPullerService()
	s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	require.NoError(t, s.Start(context.Background()))
	upstream.events <- &puller.Event{Progress: "processed"}
	upstream.events <- &puller.Event{
		Change:   &events.StoreChangeEvent{EventID: "invalid", OpType: "invalid"},
		Progress: "failed",
	}
	upstream.events <- &puller.Event{Progress: "later"}
	internal := getInternalService(s)
	waitSignal(t, internal.consumeDone)
	assert.Equal(t, "processed", internal.progress)
	require.ErrorIs(t, internal.Err(), events.ErrUnknownOpType)
	waitSignal(t, upstream.closed)
}

func TestConsumePullerEvents_CancellationDuringProcessingKeepsCheckpoint(t *testing.T) {
	upstream := newTestPullerService()
	s, err := NewService(ServerConfig{SendTimeout: time.Minute}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	_, err = stream.Subscribe("database1", "users", nil)
	require.NoError(t, err)
	local := stream.(*localStream)
	for range cap(local.outgoing) {
		local.outgoing <- &EventDelivery{}
	}
	require.NoError(t, s.Start(context.Background()))
	upstream.events <- &puller.Event{Progress: "processed"}
	waitSignal(t, upstream.requests)
	waitSignal(t, upstream.requests)
	upstream.events <- &puller.Event{
		Change: &events.StoreChangeEvent{
			EventID: "blocked", Database: "database1", OpType: events.StoreOperationInsert,
			FullDocument: testStoredDoc("users", "doc1", "database1", nil),
		},
		Progress: "not-delivered",
	}
	require.Eventually(t, func() bool { return len(upstream.events) == 0 }, time.Second, time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, s.Stop(stopCtx))
	assert.Equal(t, "processed", getInternalService(s).progress)
}

func TestConsumePullerEvents_UpstreamFailureClosesStreams(t *testing.T) {
	t.Parallel()
	upstream := newTestPullerService()
	upstream.terminalErr = errors.New("durable source unavailable")
	s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
	require.NoError(t, err)
	local, err := s.Stream(context.Background())
	require.NoError(t, err)
	internal := getInternalService(s)
	grpcCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	remoteResult := make(chan error, 1)
	go func() { remoteResult <- internal.GRPCStream(&mockBidiStream{ctx: grpcCtx}) }()
	require.Eventually(t, func() bool {
		internal.streamsMu.RLock()
		defer internal.streamsMu.RUnlock()
		return len(internal.streams) == 2
	}, time.Second, time.Millisecond)
	require.NoError(t, s.Start(context.Background()))
	close(upstream.events)
	waitSignal(t, internal.consumeDone)
	_, err = local.Recv()
	require.ErrorIs(t, err, upstream.terminalErr)
	_, err = local.Subscribe("database1", "users", nil)
	require.ErrorIs(t, err, upstream.terminalErr)
	select {
	case err := <-remoteResult:
		require.ErrorIs(t, err, upstream.terminalErr)
	case <-time.After(time.Second):
		t.Fatal("gRPC stream remained open after upstream failure")
	}
	_, err = s.Stream(context.Background())
	require.ErrorIs(t, err, upstream.terminalErr)
	require.ErrorIs(t, internal.GRPCStream(&mockBidiStream{ctx: grpcCtx}), upstream.terminalErr)
}

func TestConsumePullerEvents_CancellationClosesSubscription(t *testing.T) {
	for _, stopService := range []bool{false, true} {
		t.Run(fmt.Sprintf("stopService=%t", stopService), func(t *testing.T) {
			t.Parallel()
			upstream := newTestPullerService()
			s, err := NewService(ServerConfig{}, slog.Default(), WithPullerClient(upstream))
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			require.NoError(t, s.Start(ctx))
			waitSignal(t, upstream.requests)
			if stopService {
				stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
				defer stopCancel()
				require.NoError(t, s.Stop(stopCtx))
			} else {
				cancel()
			}
			waitSignal(t, upstream.closed)
			require.ErrorIs(t, getInternalService(s).Err(), context.Canceled)
		})
	}
}

func TestService_Subscribe_FilterCompileError(t *testing.T) {
	t.Parallel()
	// Test subscribe with invalid filter that causes compilation failure
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)

	stream, err := s.Stream(context.Background())
	require.NoError(t, err)
	defer stream.Close()

	// Subscribe with an invalid operator that will fail filter compilation
	_, err = stream.Subscribe("database1", "users", []model.Filter{
		{Field: "status", Op: model.FilterOp("invalid_operator"), Value: "active"},
	})

	// Should fail because the operator is invalid
	require.Error(t, err)
	assert.Contains(t, err.Error(), "subscribe failed")
}

func TestService_Subscribe_ManagerReturnsError(t *testing.T) {
	t.Parallel()
	// Test subscribe error handling when manager returns non-nil error
	// This tests the err != nil branch in subscribe()
	s, err := NewService(ServerConfig{}, slog.Default())
	require.NoError(t, err)
	internal := getInternalService(s)

	// Test by calling subscribe directly with an invalid gateway (empty gatewayID)
	// The manager should handle this gracefully
	_, err = internal.subscribe("test-gateway", "database1", "users", []model.Filter{
		{Field: "status", Op: model.FilterOp("invalid_op"), Value: "test"},
	})

	require.Error(t, err)
}
