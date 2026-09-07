package watcher

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"github.com/syntrixbase/syntrix/pkg/model"
)

// MockDocumentStore is a mock implementation of storage.DocumentStore
type MockDocumentStore struct {
	mock.Mock
}

func (m *MockDocumentStore) Create(ctx context.Context, database string, doc storage.StoredDoc) error {
	args := m.Called(ctx, database, doc)
	return args.Error(0)
}

func (m *MockDocumentStore) Get(ctx context.Context, database, id string) (*storage.StoredDoc, error) {
	args := m.Called(ctx, database, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.StoredDoc), args.Error(1)
}

func (m *MockDocumentStore) Update(ctx context.Context, database, id string, data map[string]interface{}, filters model.Filters) error {
	args := m.Called(ctx, database, id, data, filters)
	return args.Error(0)
}

func (m *MockDocumentStore) Patch(ctx context.Context, database, id string, data map[string]interface{}, pred model.Filters) error {
	args := m.Called(ctx, database, id, data, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) Delete(ctx context.Context, database, id string, pred model.Filters) error {
	args := m.Called(ctx, database, id, pred)
	return args.Error(0)
}

func (m *MockDocumentStore) DeleteByDatabase(ctx context.Context, database string, limit int) (int, error) {
	args := m.Called(ctx, database, limit)
	return args.Int(0), args.Error(1)
}

func (m *MockDocumentStore) Query(ctx context.Context, database string, q model.Query) ([]*storage.StoredDoc, error) {
	args := m.Called(ctx, database, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.StoredDoc), args.Error(1)
}

func (m *MockDocumentStore) GetMany(ctx context.Context, database string, paths []string) ([]*storage.StoredDoc, error) {
	args := m.Called(ctx, database, paths)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.StoredDoc), args.Error(1)
}

func (m *MockDocumentStore) Watch(ctx context.Context, database, collection string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	args := m.Called(ctx, database, collection, resumeToken, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockDocumentStore) Close(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

type MockPullerService struct {
	mock.Mock
}

func (m *MockPullerService) Subscribe(ctx context.Context, opts events.SubscribeOptions) (events.Subscription, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(events.Subscription), args.Error(1)
}

type testSubscription struct {
	events   []*events.PullerEvent
	terminal error
	closeErr error
	closed   int
}

func (s *testSubscription) Next(ctx context.Context) (*events.PullerEvent, error) {
	if len(s.events) == 0 {
		if s.terminal != nil {
			return nil, s.terminal
		}
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *testSubscription) Close() error {
	s.closed++
	return s.closeErr
}

func TestWatch_Checkpoint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		opts      WatcherOptions
		doc       *storage.StoredDoc
		getErr    error
		wantToken string
		wantErr   string
	}{
		{name: "resume", doc: &storage.StoredDoc{Data: map[string]interface{}{"token": "saved"}}, wantToken: "saved"},
		{name: "explicit fresh start", opts: WatcherOptions{StartFromNow: true}, getErr: model.ErrNotFound},
		{name: "missing checkpoint", getErr: model.ErrNotFound, wantErr: "checkpoint not found"},
		{name: "storage failure", getErr: assert.AnError, wantErr: "load trigger checkpoint"},
		{name: "missing document", wantErr: "missing from successful storage response"},
		{name: "invalid token", opts: WatcherOptions{StartFromNow: true}, doc: &storage.StoredDoc{Data: map[string]interface{}{"token": 42}}, wantErr: "nonempty string"},
		{name: "empty token", doc: &storage.StoredDoc{Data: map[string]interface{}{"token": ""}}, wantErr: "nonempty string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := new(MockDocumentStore)
			p := new(MockPullerService)
			w := NewWatcher(p, store, tc.opts)
			store.On("Get", mock.Anything, "default", checkpointKey).Return(tc.doc, tc.getErr).Once()
			if tc.wantErr == "" {
				p.On("Subscribe", mock.Anything, events.SubscribeOptions{ConsumerID: "trigger-evaluator", After: tc.wantToken}).Return(&testSubscription{}, nil).Once()
			}
			stream, err := w.Watch(context.Background())
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				if tc.getErr != nil {
					assert.ErrorIs(t, err, tc.getErr)
				}
				assert.Nil(t, stream)
			} else {
				require.NoError(t, err)
				require.NoError(t, stream.Close())
			}
			store.AssertExpectations(t)
			p.AssertExpectations(t)
		})
	}
}

func TestWatch_TransformsOrderedEventsAndProgress(t *testing.T) {
	store := new(MockDocumentStore)
	p := new(MockPullerService)
	w := NewWatcher(p, store, WatcherOptions{StartFromNow: true})
	store.On("Get", mock.Anything, "default", checkpointKey).Return(nil, model.ErrNotFound)
	upstreamErr := errors.New("history expired")
	sub := &testSubscription{terminal: upstreamErr, events: []*events.PullerEvent{
		{Change: &events.StoreChangeEvent{EventID: "a", Database: "db1", OpType: events.StoreOperationInsert, FullDocument: &storage.StoredDoc{Id: "doc1"}}, Progress: "p1"},
		{Change: &events.StoreChangeEvent{EventID: "b", Database: "db2", OpType: events.StoreOperationUpdate, FullDocument: &storage.StoredDoc{Id: "doc2"}}, Progress: "p2"},
		{Change: &events.StoreChangeEvent{EventID: "c", OpType: events.StoreOperationDelete}, Progress: "p3"},
		{Progress: "p4"},
		{Change: &events.StoreChangeEvent{EventID: "d", OpType: events.StoreOperationUpdate, FullDocument: &storage.StoredDoc{Id: "doc4", Deleted: true}}, Progress: "p5"},
		{Change: &events.StoreChangeEvent{EventID: "e", OpType: events.StoreOperationReplace, FullDocument: &storage.StoredDoc{Id: "doc5"}}, Progress: "p6"},
	}}
	p.On("Subscribe", mock.Anything, events.SubscribeOptions{ConsumerID: "trigger-evaluator"}).Return(sub, nil)
	stream, err := w.Watch(context.Background())
	require.NoError(t, err)
	defer stream.Close()
	for _, expected := range []struct {
		id, progress, database string
		kind                   events.EventType
	}{
		{"a", "p1", "db1", events.EventCreate},
		{"b", "p2", "db2", events.EventUpdate},
		{"", "p3", "", ""},
		{"", "p4", "", ""},
		{"d", "p5", "", events.EventDelete},
		{"e", "p6", "", events.EventCreate},
	} {
		evt, err := stream.Next(context.Background())
		require.NoError(t, err)
		assert.Equal(t, expected.id, evt.Id)
		assert.Equal(t, expected.progress, evt.Progress)
		assert.Equal(t, expected.database, evt.Database)
		assert.Equal(t, expected.kind, evt.Type)
	}
	_, err = stream.Next(context.Background())
	require.ErrorIs(t, err, upstreamErr)
	_, err = stream.Next(context.Background())
	require.ErrorIs(t, err, upstreamErr)
}

func TestWatch_TransformFailureIsTerminal(t *testing.T) {
	store := new(MockDocumentStore)
	p := new(MockPullerService)
	w := NewWatcher(p, store, WatcherOptions{StartFromNow: true})
	store.On("Get", mock.Anything, "default", checkpointKey).Return(nil, model.ErrNotFound)
	sub := &testSubscription{events: []*events.PullerEvent{
		{Change: &events.StoreChangeEvent{EventID: "invalid", OpType: "unsupported"}, Progress: "p1"},
		{Progress: "p2"},
	}}
	p.On("Subscribe", mock.Anything, mock.Anything).Return(sub, nil)
	stream, err := w.Watch(context.Background())
	require.NoError(t, err)
	defer stream.Close()
	for range 2 {
		_, err := stream.Next(context.Background())
		require.ErrorIs(t, err, events.ErrUnknownOpType)
	}
	assert.Len(t, sub.events, 1)
}

func TestWatch_SubscribeFailure(t *testing.T) {
	store := new(MockDocumentStore)
	p := new(MockPullerService)
	w := NewWatcher(p, store, WatcherOptions{StartFromNow: true})
	store.On("Get", mock.Anything, "default", checkpointKey).Return(nil, model.ErrNotFound)
	p.On("Subscribe", mock.Anything, mock.Anything).Return(nil, assert.AnError)
	stream, err := w.Watch(context.Background())
	require.ErrorIs(t, err, assert.AnError)
	assert.Nil(t, stream)
}

func TestWatcherClose_ClosesOwnedStreamsAndRejectsRegistration(t *testing.T) {
	store := new(MockDocumentStore)
	p := new(MockPullerService)
	w := NewWatcher(p, store, WatcherOptions{StartFromNow: true})
	store.On("Get", mock.Anything, "default", checkpointKey).Return(nil, model.ErrNotFound)
	sub := &testSubscription{closeErr: assert.AnError}
	p.On("Subscribe", mock.Anything, mock.Anything).Return(sub, nil).Once()
	stream, err := w.Watch(context.Background())
	require.NoError(t, err)
	require.ErrorIs(t, w.Close(), assert.AnError)
	require.ErrorIs(t, w.Close(), assert.AnError)
	require.ErrorIs(t, stream.Close(), assert.AnError)
	assert.Equal(t, 1, sub.closed)
	newSub := &testSubscription{}
	p.On("Subscribe", mock.Anything, mock.Anything).Return(newSub, nil).Once()
	_, err = w.Watch(context.Background())
	require.ErrorContains(t, err, "watcher is closed")
	assert.Equal(t, 1, newSub.closed)
}

func TestSaveCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		updateErr, createErr error
	}{
		{name: "update"},
		{name: "create", updateErr: model.ErrNotFound},
		{name: "update error", updateErr: assert.AnError},
		{name: "create error", updateErr: model.ErrNotFound, createErr: assert.AnError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := new(MockDocumentStore)
			w := NewWatcher(new(MockPullerService), store, WatcherOptions{CheckpointDatabase: "metadata"})
			store.On("Update", mock.Anything, "metadata", checkpointKey, mock.MatchedBy(func(data map[string]interface{}) bool { return data["token"] == "new-token" }), mock.Anything).Return(tc.updateErr).Once()
			if errors.Is(tc.updateErr, model.ErrNotFound) {
				store.On("Create", mock.Anything, "metadata", mock.Anything).Return(tc.createErr).Once()
			}
			err := w.SaveCheckpoint(context.Background(), "new-token")
			if tc.updateErr == assert.AnError || tc.createErr != nil {
				assert.ErrorIs(t, err, assert.AnError)
			} else {
				assert.NoError(t, err)
			}
			store.AssertExpectations(t)
		})
	}
}
