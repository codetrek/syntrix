package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockQueryService is a mock implementation of query.Service
type MockQueryService struct {
	mock.Mock
}

func (m *MockQueryService) GetDocument(ctx context.Context, path string) (*storage.Document, error) {
	args := m.Called(ctx, path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockQueryService) CreateDocument(ctx context.Context, doc *storage.Document) error {
	args := m.Called(ctx, doc)
	return args.Error(0)
}

func (m *MockQueryService) UpdateDocument(ctx context.Context, path string, data map[string]interface{}, version int64) error {
	args := m.Called(ctx, path, data, version)
	return args.Error(0)
}

func (m *MockQueryService) ReplaceDocument(ctx context.Context, path string, collection string, data map[string]interface{}) (*storage.Document, error) {
	args := m.Called(ctx, path, collection, data)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockQueryService) PatchDocument(ctx context.Context, path string, data map[string]interface{}) (*storage.Document, error) {
	args := m.Called(ctx, path, data)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.Document), args.Error(1)
}

func (m *MockQueryService) DeleteDocument(ctx context.Context, path string) error {
	args := m.Called(ctx, path)
	return args.Error(0)
}

func (m *MockQueryService) ExecuteQuery(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	args := m.Called(ctx, q)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*storage.Document), args.Error(1)
}

func (m *MockQueryService) WatchCollection(ctx context.Context, collection string) (<-chan storage.Event, error) {
	args := m.Called(ctx, collection)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockQueryService) Pull(ctx context.Context, req storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPullResponse), args.Error(1)
}

func (m *MockQueryService) Push(ctx context.Context, req storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.ReplicationPushResponse), args.Error(1)
}

func TestHandleGetDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService)

	doc := &storage.Document{
		Id:         "rooms/room-1/messages/msg-1",
		Collection: "rooms/room-1/messages",
		Data:       map[string]interface{}{"name": "Alice"},
		Version:    1,
	}

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/msg-1").Return(doc, nil)

	req, _ := http.NewRequest("GET", "/v1/rooms/room-1/messages/msg-1", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	// Path is not returned in flattened response unless it's in data.id
	// assert.Equal(t, "rooms/room-1/messages/msg-1", resp.Path)
	assert.Equal(t, "Alice", resp["name"])
}

func TestHandleCreateDocument(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService)

	// Note: The API server might be calling CreateDocument or ReplaceDocument depending on implementation.
	// Assuming it calls CreateDocument for POST /v1/collection
	mockService.On("CreateDocument", mock.Anything, mock.AnythingOfType("*storage.Document")).Return(nil)

	body := []byte(`{"name": "Bob"}`)
	req, _ := http.NewRequest("POST", "/v1/rooms/room-1/messages", bytes.NewBuffer(body))
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)
}

func TestHandleGetDocument_NotFound(t *testing.T) {
	mockService := new(MockQueryService)
	server := NewServer(mockService)

	mockService.On("GetDocument", mock.Anything, "rooms/room-1/messages/unknown").Return(nil, storage.ErrNotFound)

	req, _ := http.NewRequest("GET", "/v1/rooms/room-1/messages/unknown", nil)
	rr := httptest.NewRecorder()

	server.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}
