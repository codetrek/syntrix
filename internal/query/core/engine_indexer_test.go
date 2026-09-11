package core

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/indexer"
	"github.com/syntrixbase/syntrix/pkg/model"
)

// MockIndexerService is a mock implementation of indexer.Service
type MockIndexerService struct {
	mock.Mock
}

func (m *MockIndexerService) Search(ctx context.Context, database string, plan indexer.Plan) ([]indexer.DocRef, error) {
	args := m.Called(ctx, database, plan)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]indexer.DocRef), args.Error(1)
}

func (m *MockIndexerService) Health(ctx context.Context) (indexer.Health, error) {
	args := m.Called(ctx)
	return args.Get(0).(indexer.Health), args.Error(1)
}

func (m *MockIndexerService) Stats(ctx context.Context) (indexer.Stats, error) {
	args := m.Called(ctx)
	return args.Get(0).(indexer.Stats), args.Error(1)
}

func TestEngine_ExecuteQuery_Success(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(MockStorageBackend)
	mockIndexer := new(MockIndexerService)

	engine := New(mockStorage, mockIndexer)

	// Indexer returns document references with IDs
	refs := []indexer.DocRef{
		{ID: "user1"},
		{ID: "user2"},
	}
	mockIndexer.On("Search", ctx, "testdb", mock.AnythingOfType("manager.Plan")).Return(refs, nil)

	// Storage returns documents
	storedDocs := []*storage.StoredDoc{
		{
			Id:       "testdb:users/user1",
			Fullpath: "users/user1",
			Data:     map[string]interface{}{"name": "Alice", "age": float64(30)},
		},
		{
			Id:       "testdb:users/user2",
			Fullpath: "users/user2",
			Data:     map[string]interface{}{"name": "Bob", "age": float64(25)},
		},
	}
	mockStorage.On("GetMany", ctx, "testdb", []string{"users/user1", "users/user2"}).Return(storedDocs, nil)

	query := model.Query{
		Collection: "users",
		OrderBy:    []model.Order{{Field: "age", Direction: "asc"}},
		Limit:      10,
	}

	docs, err := engine.ExecuteQuery(ctx, "testdb", query)

	assert.NoError(t, err)
	assert.Len(t, docs, 2)

	mockIndexer.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ExecuteQuery_IndexerError(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(MockStorageBackend)
	mockIndexer := new(MockIndexerService)

	engine := New(mockStorage, mockIndexer)

	// Indexer returns error
	mockIndexer.On("Search", ctx, "testdb", mock.AnythingOfType("manager.Plan")).
		Return(nil, errors.New("no matching index"))

	query := model.Query{
		Collection: "users",
		Filters:    []model.Filter{{Field: "status", Op: "==", Value: "active"}}, // Filter to trigger indexer path
		Limit:      10,
	}

	docs, err := engine.ExecuteQuery(ctx, "testdb", query)

	assert.Error(t, err)
	assert.Nil(t, docs)
	assert.Equal(t, "no matching index", err.Error())

	mockIndexer.AssertExpectations(t)
	// Storage should NOT be called
	mockStorage.AssertExpectations(t)
}

func TestEngine_ExecuteQuery_EmptyResults(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(MockStorageBackend)
	mockIndexer := new(MockIndexerService)

	engine := New(mockStorage, mockIndexer)

	refs := []indexer.DocRef{}
	mockIndexer.On("Search", ctx, "testdb", mock.AnythingOfType("manager.Plan")).Return(refs, nil)

	query := model.Query{
		Collection: "users",
		OrderBy:    []model.Order{{Field: "age", Direction: "asc"}}, // OrderBy to trigger indexer path
		Limit:      10,
	}

	docs, err := engine.ExecuteQuery(ctx, "testdb", query)

	assert.NoError(t, err)
	assert.Len(t, docs, 0)

	mockIndexer.AssertExpectations(t)
}

func TestEngine_ExecuteQuery_GetManyError(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(MockStorageBackend)
	mockIndexer := new(MockIndexerService)

	engine := New(mockStorage, mockIndexer)

	refs := []indexer.DocRef{{ID: "user1"}}
	mockIndexer.On("Search", ctx, "testdb", mock.AnythingOfType("manager.Plan")).Return(refs, nil)

	// GetMany fails
	mockStorage.On("GetMany", ctx, "testdb", []string{"users/user1"}).
		Return(nil, errors.New("db error"))

	query := model.Query{
		Collection: "users",
		Filters:    []model.Filter{{Field: "status", Op: "==", Value: "active"}}, // Filter to trigger indexer path
		Limit:      10,
	}

	docs, err := engine.ExecuteQuery(ctx, "testdb", query)

	assert.Error(t, err)
	assert.Nil(t, docs)
	assert.Equal(t, "db error", err.Error())

	mockIndexer.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestEngine_ExecuteQuery_NilDocumentsFiltered(t *testing.T) {
	ctx := context.Background()
	mockStorage := new(MockStorageBackend)
	mockIndexer := new(MockIndexerService)

	engine := New(mockStorage, mockIndexer)

	refs := []indexer.DocRef{
		{ID: "user1"},
		{ID: "user2"}, // This one is missing
		{ID: "user3"},
	}
	mockIndexer.On("Search", ctx, "testdb", mock.AnythingOfType("manager.Plan")).Return(refs, nil)

	// GetMany returns nil for missing doc
	storedDocs := []*storage.StoredDoc{
		{
			Id:       "testdb:users/user1",
			Fullpath: "users/user1",
			Data:     map[string]interface{}{"name": "Alice"},
		},
		nil, // user2 not found
		{
			Id:       "testdb:users/user3",
			Fullpath: "users/user3",
			Data:     map[string]interface{}{"name": "Charlie"},
		},
	}
	mockStorage.On("GetMany", ctx, "testdb", []string{"users/user1", "users/user2", "users/user3"}).Return(storedDocs, nil)

	query := model.Query{
		Collection: "users",
		Filters:    []model.Filter{{Field: "status", Op: "==", Value: "active"}}, // Filter to trigger indexer path
		Limit:      10,
	}

	docs, err := engine.ExecuteQuery(ctx, "testdb", query)

	assert.NoError(t, err)
	assert.Len(t, docs, 2) // Only non-nil docs

	mockIndexer.AssertExpectations(t)
	mockStorage.AssertExpectations(t)
}

func TestEngine_QueryToPlan(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, nil)

	query := model.Query{
		Collection: "users",
		Filters: model.Filters{
			{Field: "status", Op: "==", Value: "active"},
			{Field: "age", Op: ">", Value: 18},
			{Field: "score", Op: ">=", Value: 100},
			{Field: "level", Op: "<", Value: 10},
			{Field: "rank", Op: "<=", Value: 5},
		},
		OrderBy:    []model.Order{{Field: "createdAt", Direction: "desc"}},
		StartAfter: "cursor123",
		Limit:      50,
	}

	plan, err := engine.queryToPlan(query)
	require.NoError(t, err)

	assert.Equal(t, "users", plan.Collection)
	assert.Equal(t, 50, plan.Limit)
	assert.Len(t, plan.Filters, 5)
	assert.Len(t, plan.OrderBy, 1)
	assert.Equal(t, "cursor123", plan.StartAfter)
	assert.Equal(t, "createdAt", plan.OrderBy[0].Field)
	assert.Equal(t, indexer.Desc, plan.OrderBy[0].Direction)

	// Verify filter ops
	assert.Equal(t, indexer.FilterEq, plan.Filters[0].Op)
	assert.Equal(t, indexer.FilterGt, plan.Filters[1].Op)
	assert.Equal(t, indexer.FilterGte, plan.Filters[2].Op)
	assert.Equal(t, indexer.FilterLt, plan.Filters[3].Op)
	assert.Equal(t, indexer.FilterLte, plan.Filters[4].Op)
}

func TestEngine_QueryToPlan_Ascending(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, nil)

	query := model.Query{
		Collection: "users",
		OrderBy:    []model.Order{{Field: "name", Direction: "asc"}},
		Limit:      10,
	}

	plan, err := engine.queryToPlan(query)
	require.NoError(t, err)

	assert.Len(t, plan.OrderBy, 1)
	assert.Equal(t, "name", plan.OrderBy[0].Field)
	assert.Equal(t, indexer.Asc, plan.OrderBy[0].Direction)
}

func TestEngine_ExecuteQuery_UnsupportedOperators(t *testing.T) {
	for _, op := range []model.FilterOp{model.OpNe, model.OpIn, model.OpContains, "unknown"} {
		for _, mixed := range []bool{false, true} {
			name := string(op) + "/single"
			if mixed {
				name = string(op) + "/mixed"
			}
			t.Run(name, func(t *testing.T) {
				mockStorage := new(MockStorageBackend)
				mockIndexer := new(MockIndexerService)
				engine := New(mockStorage, mockIndexer)
				var value any = "private_value"
				if op == model.OpIn {
					value = []string{"private_value"}
				}
				filters := model.Filters{{Field: "private_field", Op: op, Value: value}}
				if mixed {
					filters = append(model.Filters{{Field: "status", Op: model.OpEq, Value: "active"}}, filters...)
					filters = append(filters, model.Filter{Field: "age", Op: model.OpGte, Value: 18})
				}

				docs, err := engine.ExecuteQuery(context.Background(), "testdb", model.Query{
					Collection: "users",
					Filters:    filters,
					Limit:      10,
				})

				require.ErrorIs(t, err, model.ErrInvalidQuery)
				assert.Nil(t, docs)
				assert.Contains(t, err.Error(), `"`+string(op)+`"`)
				assert.NotContains(t, err.Error(), "private_field")
				assert.NotContains(t, err.Error(), "private_value")
				assert.Empty(t, mockIndexer.Calls)
				assert.Empty(t, mockStorage.Calls)
			})
		}
	}
}

func TestEngine_ExecuteQuery_IDMembershipRequiresSupportedIndexedPlan(t *testing.T) {
	for _, q := range []model.Query{
		{
			Collection: "users",
			Filters:    model.Filters{{Field: "id", Op: model.OpIn, Value: []string{"user1"}}},
			OrderBy:    []model.Order{{Field: "name", Direction: "asc"}},
		},
		{
			Collection: "users",
			Filters: model.Filters{
				{Field: "id", Op: model.OpIn, Value: []string{"user1"}},
				{Field: "status", Op: model.OpEq, Value: "active"},
			},
		},
	} {
		mockStorage := new(MockStorageBackend)
		mockIndexer := new(MockIndexerService)
		docs, err := New(mockStorage, mockIndexer).ExecuteQuery(context.Background(), "testdb", q)
		require.ErrorIs(t, err, model.ErrInvalidQuery)
		assert.Nil(t, docs)
		assert.Empty(t, mockIndexer.Calls)
		assert.Empty(t, mockStorage.Calls)
	}
}

func TestEngine_ExecuteQuery_IDFiltersUseStorage(t *testing.T) {
	for _, filters := range []model.Filters{
		{{Field: "id", Op: model.OpEq, Value: "user1"}},
		{{Field: "id", Op: model.OpIn, Value: []string{"user1", "user2"}}},
		{
			{Field: "id", Op: model.OpEq, Value: "user1"},
			{Field: "id", Op: model.OpIn, Value: []string{"user1", "user2"}},
		},
	} {
		mockStorage := new(MockStorageBackend)
		q := model.Query{Collection: "users", Filters: filters, Limit: 10}
		stored := storage.NewStoredDoc("testdb", "users", "user1", map[string]interface{}{"name": "Alice"})
		mockStorage.On("Query", mock.Anything, "testdb", q).Return([]*storage.StoredDoc{&stored}, nil).Once()

		docs, err := New(mockStorage, nil).ExecuteQuery(context.Background(), "testdb", q)

		require.NoError(t, err)
		require.Len(t, docs, 1)
		assert.Equal(t, "user1", docs[0].GetID())
		mockStorage.AssertExpectations(t)
	}
}

func TestEngine_ExecuteQuery_RequiresIndexerBeforePlanning(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	docs, err := New(mockStorage, nil).ExecuteQuery(context.Background(), "testdb", model.Query{
		Collection: "users",
		Filters:    model.Filters{{Field: "role", Op: model.OpIn, Value: []string{"admin"}}},
	})
	assert.ErrorIs(t, err, ErrIndexerRequired)
	assert.Nil(t, docs)
	assert.Empty(t, mockStorage.Calls)
}

func TestEngine_IsIDOnlyQuery(t *testing.T) {
	mockStorage := new(MockStorageBackend)
	engine := New(mockStorage, nil)

	tests := []struct {
		name     string
		query    model.Query
		expected bool
	}{
		{
			name: "id == filter only",
			query: model.Query{
				Collection: "users",
				Filters:    []model.Filter{{Field: "id", Op: "==", Value: "user1"}},
			},
			expected: true,
		},
		{
			name: "id in filter only",
			query: model.Query{
				Collection: "users",
				Filters:    []model.Filter{{Field: "id", Op: "in", Value: []string{"user1", "user2"}}},
			},
			expected: true,
		},
		{
			name: "multiple id filters",
			query: model.Query{
				Collection: "users",
				Filters: []model.Filter{
					{Field: "id", Op: "==", Value: "user1"},
					{Field: "id", Op: "in", Value: []string{"user2"}},
				},
			},
			expected: true,
		},
		{
			name: "id filter with orderBy",
			query: model.Query{
				Collection: "users",
				Filters:    []model.Filter{{Field: "id", Op: "==", Value: "user1"}},
				OrderBy:    []model.Order{{Field: "name", Direction: "asc"}},
			},
			expected: false,
		},
		{
			name: "non-id filter",
			query: model.Query{
				Collection: "users",
				Filters:    []model.Filter{{Field: "status", Op: "==", Value: "active"}},
			},
			expected: false,
		},
		{
			name: "mixed id and non-id filters",
			query: model.Query{
				Collection: "users",
				Filters: []model.Filter{
					{Field: "id", Op: "==", Value: "user1"},
					{Field: "status", Op: "==", Value: "active"},
				},
			},
			expected: false,
		},
		{
			name: "id filter with unsupported op",
			query: model.Query{
				Collection: "users",
				Filters:    []model.Filter{{Field: "id", Op: ">", Value: "user1"}},
			},
			expected: false,
		},
		{
			name: "no filters",
			query: model.Query{
				Collection: "users",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.isIDOnlyQuery(tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}
