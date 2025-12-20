package query

import (
	"context"
	"testing"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestEngine_GetDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	expectedDoc := &storage.Document{Id: "test/1", Data: map[string]interface{}{"foo": "bar"}}
	mockStorage.On("Get", ctx, "test/1").Return(expectedDoc, nil)

	doc, err := engine.GetDocument(ctx, "test/1")
	assert.NoError(t, err)
	assert.Equal(t, expectedDoc, doc)
	mockStorage.AssertExpectations(t)
}

func TestEngine_CreateDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/1", Data: map[string]interface{}{"foo": "bar"}}
	mockStorage.On("Create", ctx, doc).Return(nil)

	err := engine.CreateDocument(ctx, doc)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_Create(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := map[string]interface{}{"foo": "bar"}

	// Simulate Not Found -> Create
	mockStorage.On("Get", ctx, path).Return(nil, storage.ErrNotFound)
	mockStorage.On("Create", ctx, mock.AnythingOfType("*storage.Document")).Return(nil)

	doc, err := engine.ReplaceDocument(ctx, path, collection, data, storage.Filters{})
	assert.NoError(t, err)
	assert.Equal(t, storage.CalculateID(path), doc.Id)
	assert.Equal(t, data, doc.Data)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_Update(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := map[string]interface{}{"foo": "new"}
	existingDoc := &storage.Document{Id: path, Version: 1, Data: map[string]interface{}{"foo": "old"}}

	// Simulate Found -> Update -> Get
	mockStorage.On("Get", ctx, path).Return(existingDoc, nil).Once()
	mockStorage.On("Update", ctx, path, data, storage.Filters{}).Return(nil)
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: data}, nil).Once()

	doc, err := engine.ReplaceDocument(ctx, path, collection, data, storage.Filters{})
	assert.NoError(t, err)
	assert.Equal(t, data, doc.Data)
	mockStorage.AssertExpectations(t)
}

func TestEngine_PatchDocument(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	patchData := map[string]interface{}{"bar": "baz"}

	// Expect Patch call
	mockStorage.On("Patch", ctx, path, patchData, storage.Filters{}).Return(nil).Once()

	expectedMergedData := map[string]interface{}{"foo": "old", "bar": "baz"}
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: expectedMergedData}, nil).Once()

	doc, err := engine.PatchDocument(ctx, path, patchData, storage.Filters{})
	assert.NoError(t, err)
	assert.Equal(t, expectedMergedData, doc.Data)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ReplaceDocument_IfMatch(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	collection := "test"
	data := map[string]interface{}{"foo": "new"}
	existingDoc := &storage.Document{Id: path, Version: 1, Data: map[string]interface{}{"foo": "old"}}

	filters := storage.Filters{
		{Field: "version", Op: "==", Value: int64(1)},
	}

	// Simulate Found -> Update -> Get
	mockStorage.On("Get", ctx, path).Return(existingDoc, nil).Once()
	mockStorage.On("Update", ctx, path, data, filters).Return(nil)
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: data}, nil).Once()

	doc, err := engine.ReplaceDocument(ctx, path, collection, data, filters)
	assert.NoError(t, err)
	assert.Equal(t, data, doc.Data)
	mockStorage.AssertExpectations(t)
}

func TestEngine_PatchDocument_IfMatch(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	path := "test/1"
	patchData := map[string]interface{}{"bar": "baz"}

	filters := storage.Filters{
		{Field: "foo", Op: "==", Value: "old"},
	}

	// Expect Patch call with filters
	mockStorage.On("Patch", ctx, path, patchData, filters).Return(nil).Once()

	expectedMergedData := map[string]interface{}{"foo": "old", "bar": "baz"}
	mockStorage.On("Get", ctx, path).Return(&storage.Document{Id: path, Version: 2, Data: expectedMergedData}, nil).Once()

	doc, err := engine.PatchDocument(ctx, path, patchData, filters)
	assert.NoError(t, err)
	assert.Equal(t, expectedMergedData, doc.Data)
	mockStorage.AssertExpectations(t)
}

func TestEngine_Pull(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	req := storage.ReplicationPullRequest{
		Collection: "test",
		Checkpoint: 100,
		Limit:      10,
	}

	expectedDocs := []*storage.Document{
		{Id: "test/1", UpdatedAt: 101},
		{Id: "test/2", UpdatedAt: 102},
	}

	mockStorage.On("Query", ctx, mock.MatchedBy(func(q storage.Query) bool {
		return q.Collection == "test" && q.Limit == 10 && q.Filters[0].Value == int64(100)
	})).Return(expectedDocs, nil)

	resp, err := engine.Pull(ctx, req)
	assert.NoError(t, err)
	assert.Equal(t, expectedDocs, resp.Documents)
	assert.Equal(t, int64(102), resp.Checkpoint)
	mockStorage.AssertExpectations(t)
}

func TestEngine_Push(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := NewEngine(mockStorage, "http://mock-csp")
	ctx := context.Background()

	doc := &storage.Document{Id: "test/1", Collection: "test", Data: map[string]interface{}{"foo": "bar"}, Version: 1}
	req := storage.ReplicationPushRequest{
		Collection: "test",
		Changes: []storage.ReplicationPushChange{
			{Doc: doc},
		},
	}

	// Simulate Get -> Update (Success)
	existingDoc := &storage.Document{Id: "test/1", Version: 1}
	mockStorage.On("Get", ctx, "test/1").Return(existingDoc, nil)
	mockStorage.On("Update", ctx, "test/1", doc.Data, storage.Filters{}).Return(nil)

	resp, err := engine.Push(ctx, req)
	assert.NoError(t, err)
	assert.Empty(t, resp.Conflicts)
	mockStorage.AssertExpectations(t)
}
