package query

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEngine_ExecuteQuery(t *testing.T) {
	type testCase struct {
		name        string
		query       model.Query
		mockSetup   func(*MockStorageBackend)
		expectedDocs []model.Document
		expectError bool
	}

	tests := []testCase{
		{
			name: "Success",
			query: model.Query{
				Collection: "test",
				Filters: []model.Filter{
					{Field: "foo", Op: "==", Value: "bar"},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				storedDocs := []*storage.Document{
					{
						Fullpath:   "test/1",
						Collection: "test",
						Data:       map[string]interface{}{"foo": "bar"},
						Version:    1,
						UpdatedAt:  100,
						CreatedAt:  90,
					},
				}
				m.On("Query", mock.Anything, "default", mock.MatchedBy(func(q model.Query) bool {
					return q.Collection == "test" && q.Filters[0].Value == "bar"
				})).Return(storedDocs, nil)
			},
			expectedDocs: []model.Document{
				{
					"id":         "1",
					"collection": "test",
					"foo":        "bar",
					"version":    int64(1),
					"updatedAt":  int64(100),
					"createdAt":  int64(90),
				},
			},
			expectError: false,
		},
		{
			name: "Storage Error",
			query: model.Query{
				Collection: "test",
			},
			mockSetup: func(m *MockStorageBackend) {
				m.On("Query", mock.Anything, "default", mock.Anything).Return(nil, assert.AnError)
			},
			expectError: true,
		},
		{
			name: "Empty Result",
			query: model.Query{
				Collection: "test",
			},
			mockSetup: func(m *MockStorageBackend) {
				m.On("Query", mock.Anything, "default", mock.Anything).Return([]*storage.Document{}, nil)
			},
			expectedDocs: []model.Document{},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := new(MockStorageBackend)
			if tc.mockSetup != nil {
				tc.mockSetup(mockStorage)
			}
			engine := NewEngine(mockStorage, "http://mock-csp")
			ctx := context.Background()

			docs, err := engine.ExecuteQuery(ctx, "default", tc.query)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedDocs), len(docs))
				for i, d := range tc.expectedDocs {
					assert.Equal(t, d, docs[i])
				}
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestEngine_Push_Coverage(t *testing.T) {
	type testCase struct {
		name              string
		req               storage.ReplicationPushRequest
		mockSetup         func(*MockStorageBackend)
		expectedConflicts []*storage.Document
		expectError       bool
	}

	tests := []testCase{
		{
			name: "Conflict (Version Mismatch)",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc:         &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 2},
						BaseVersion: ptr(int64(1)),
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				// Existing doc has version 3, but we expect base version 1 -> Conflict
				existingDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 3, Data: map[string]interface{}{"foo": "old"}}
				m.On("Get", mock.Anything, "default", "test/1").Return(existingDoc, nil)
			},
			expectedConflicts: []*storage.Document{
				{Id: "test/1", Fullpath: "test/1", Version: 3, Data: map[string]interface{}{"foo": "old"}},
			},
			expectError: false,
		},
		{
			name: "Delete Success",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc:         &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Deleted: true, Version: 2},
						BaseVersion: ptr(int64(1)),
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				existingDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 1}
				m.On("Get", mock.Anything, "default", "test/1").Return(existingDoc, nil)
				m.On("Delete", mock.Anything, "default", "test/1", mock.MatchedBy(func(f model.Filters) bool {
					return f[0].Field == "version" && f[0].Value == int64(1)
				})).Return(nil)
			},
			expectedConflicts: nil,
			expectError:       false,
		},
		{
			name: "Delete Conflict (Precondition Failed)",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc:         &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Deleted: true, Version: 2},
						BaseVersion: ptr(int64(1)),
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				existingDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 1}
				m.On("Get", mock.Anything, "default", "test/1").Return(existingDoc, nil).Once()

				// Delete fails with PreconditionFailed
				m.On("Delete", mock.Anything, "default", "test/1", mock.Anything).Return(model.ErrPreconditionFailed)

				// Fetch latest for conflict
				latestDoc := &storage.Document{Id: "test/1", Fullpath: "test/1", Version: 3}
				m.On("Get", mock.Anything, "default", "test/1").Return(latestDoc, nil).Once()
			},
			expectedConflicts: []*storage.Document{
				{Id: "test/1", Fullpath: "test/1", Version: 3},
			},
			expectError: false,
		},
		{
			name: "Create Conflict (Already Exists)",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{
						Doc: &storage.Document{Id: "test/1", Fullpath: "test/1", Collection: "test", Version: 1},
					},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				// Get returns NotFound, so we try to Create
				m.On("Get", mock.Anything, "default", "test/1").Return(nil, model.ErrNotFound)
				// Create fails (maybe race condition)
				m.On("Create", mock.Anything, "default", mock.Anything).Return(assert.AnError)
			},
			expectedConflicts: []*storage.Document{
				{Id: "test/1", Fullpath: "test/1", Collection: "test", Version: 1},
			},
			expectError: false,
		},
		{
			name: "Get Error",
			req: storage.ReplicationPushRequest{
				Collection: "test",
				Changes: []storage.ReplicationPushChange{
					{Doc: &storage.Document{Id: "test/1", Fullpath: "test/1"}},
				},
			},
			mockSetup: func(m *MockStorageBackend) {
				m.On("Get", mock.Anything, "default", "test/1").Return(nil, assert.AnError)
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := new(MockStorageBackend)
			if tc.mockSetup != nil {
				tc.mockSetup(mockStorage)
			}
			engine := NewEngine(mockStorage, "http://mock-csp")
			ctx := context.Background()

			resp, err := engine.Push(ctx, "default", tc.req)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, len(tc.expectedConflicts), len(resp.Conflicts))
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func ptr(i int64) *int64 {
	return &i
}

func TestFlattenStorageDocument_Nil(t *testing.T) {
	res := flattenStorageDocument(nil)
	assert.Nil(t, res)
}

func TestFlattenStorageDocument_Deleted(t *testing.T) {
	doc := &storage.Document{
		Fullpath:   "col/doc1",
		Collection: "col",
		Data:       map[string]interface{}{"foo": "bar"},
		Deleted:    true,
	}
	res := flattenStorageDocument(doc)
	assert.True(t, res["deleted"].(bool))
}

func TestExtractIDFromFullpath_Invalid(t *testing.T) {
	id := extractIDFromFullpath("col")
	assert.Equal(t, "", id)
}

func TestExtractIDFromFullpath_Valid(t *testing.T) {
	id := extractIDFromFullpath("col/doc1")
	assert.Equal(t, "doc1", id)
}

func TestReplaceDocument_StorageError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns error
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, errors.New("db error"))

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

func TestReplaceDocument_CreateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns NotFound
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, model.ErrNotFound)
	// Create returns error
	mockStorage.On("Create", mock.Anything, "default", mock.Anything).Return(errors.New("create error"))

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create error")
}

func TestReplaceDocument_UpdateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns success
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{}, nil).Once()
	// Update returns error
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(errors.New("update error"))

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "update error")
}

func TestReplaceDocument_GetAfterUpdateError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	doc := model.Document{"id": "doc1", "collection": "col", "foo": "bar"}

	// Get returns success
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{}, nil).Once()
	// Update returns success
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(nil)
	// Get after update returns error
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(nil, errors.New("get error")).Once()

	_, err := engine.ReplaceDocument(context.Background(), "default", doc, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get error")
}

func TestWatchCollection_RequestError(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://invalid-url")

	// This should fail creating request or doing request
	// Since we use http.NewRequestWithContext, invalid URL might not fail immediately if it's just scheme/host
	// But client.Do will fail

	_, err := engine.WatchCollection(context.Background(), "default", "col")
	assert.Error(t, err)
}

func TestWatchCollection_BadStatus(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")

	// Mock HTTP Client
	mockTransport := &MockTransport{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       http.NoBody,
			}, nil
		},
	}
	engine.SetHTTPClient(&http.Client{Transport: mockTransport})

	_, err := engine.WatchCollection(context.Background(), "default", "col")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "csp watch failed")
}

func TestPull_QueryEmpty(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	mockStorage.On("Query", mock.Anything, "default", mock.Anything).Return(nil, nil)

	resp, err := engine.Pull(context.Background(), "default", storage.ReplicationPullRequest{
		Collection: "col",
		Checkpoint: 100,
		Limit:      10,
	})

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.Documents)
	assert.Equal(t, int64(100), resp.Checkpoint)
}

func TestPush_DeleteNotFound(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Deleted:  true,
				},
			},
		},
	}

	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{Version: 1}, nil)
	mockStorage.On("Delete", mock.Anything, "default", "col/doc1", mock.Anything).Return(model.ErrNotFound)

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
}

func TestPush_UpdateConflict(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	baseVer := int64(1)
	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
				BaseVersion: &baseVer,
			},
		},
	}

	// Get returns existing doc with version 2 (conflict)
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  2,
		Data:     map[string]interface{}{"foo": "baz"},
	}, nil)

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Conflicts, 1)
	assert.Equal(t, int64(2), resp.Conflicts[0].Version)
}

func TestPush_UpdatePreconditionFailed(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "")

	baseVer := int64(1)
	req := storage.ReplicationPushRequest{
		Collection: "col",
		Changes: []storage.ReplicationPushChange{
			{
				Doc: &storage.Document{
					Fullpath: "col/doc1",
					Data:     map[string]interface{}{"foo": "bar"},
				},
				BaseVersion: &baseVer,
			},
		},
	}

	// Get returns existing doc with version 1 (match)
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  1,
	}, nil).Once()

	// Update fails with PreconditionFailed (race condition)
	mockStorage.On("Update", mock.Anything, "default", "col/doc1", mock.Anything, mock.Anything).Return(model.ErrPreconditionFailed)

	// Fetch latest for conflict
	mockStorage.On("Get", mock.Anything, "default", "col/doc1").Return(&storage.Document{
		Fullpath: "col/doc1",
		Version:  2,
	}, nil).Once()

	resp, err := engine.Push(context.Background(), "default", req)
	assert.NoError(t, err)
	assert.Len(t, resp.Conflicts, 1)
	assert.Equal(t, int64(2), resp.Conflicts[0].Version)
}

// MockTransport for HTTP Client
type MockTransport struct {
	RoundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.RoundTripFunc(req)
}
