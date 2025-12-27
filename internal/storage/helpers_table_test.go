package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCalculateID_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"Simple Path", "users/bob"},
		{"Deep Path", "users/bob/posts/1"},
		{"Empty Path", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := CalculateID(tt.path)
			assert.Len(t, id, 32)
		})
	}
}

func TestNewDocument_TableDriven(t *testing.T) {
	type testCase struct {
		name       string
		tenant     string
		path       string
		collection string
		data       map[string]interface{}
	}

	tests := []testCase{
		{
			name:       "Standard Document",
			tenant:     "default",
			path:       "users/bob",
			collection: "users",
			data:       map[string]interface{}{"foo": "bar"},
		},
		{
			name:       "Empty Data",
			tenant:     "t1",
			path:       "logs/1",
			collection: "logs",
			data:       map[string]interface{}{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now().UnixMilli()
			doc := NewDocument(tc.tenant, tc.path, tc.collection, tc.data)
			after := time.Now().UnixMilli()

			assert.Equal(t, CalculateTenantID(tc.tenant, tc.path), doc.Id)
			assert.Equal(t, tc.collection, doc.Collection)
			assert.Equal(t, tc.data, doc.Data)
			assert.Equal(t, int64(1), doc.Version)
			assert.Equal(t, tc.tenant, doc.TenantID)
			assert.Equal(t, doc.CreatedAt, doc.UpdatedAt)

			// Check timestamp is within reasonable range
			assert.GreaterOrEqual(t, doc.UpdatedAt, before)
			assert.LessOrEqual(t, doc.UpdatedAt, after)
		})
	}
}
