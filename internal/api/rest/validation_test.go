package rest

import (
	"testing"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
)

func TestValidatePathSyntax(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid path", "users/alice", false},
		{"valid nested path", "rooms/room1/messages/msg1", false},
		{"empty path", "", true},
		{"invalid chars", "users/alice!", true},
		{"starts with slash", "/users/alice", true},
		{"ends with slash", "users/alice/", true},
		{"double slash", "users//alice", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePathSyntax(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateDocumentPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid document path", "users/alice", false},
		{"valid nested document path", "rooms/room1/messages/msg1", false},
		{"invalid collection path", "users", true},
		{"invalid nested collection path", "rooms/room1/messages", true},
		{"invalid syntax", "/users/alice", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDocumentPath(tt.path)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateCollection(t *testing.T) {
	tests := []struct {
		name       string
		collection string
		wantErr    bool
	}{
		{"valid collection", "users", false},
		{"valid nested collection", "rooms/room1/messages", false},
		{"invalid document path", "users/alice", true},
		{"invalid nested document path", "rooms/room1/messages/msg1", true},
		{"invalid syntax", "/users", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCollection(tt.collection)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateData(t *testing.T) {
	tests := []struct {
		name    string
		data    model.Document
		wantErr bool
	}{
		{"valid data", model.Document{"key": "value"}, false},
		{"nil data", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.data.ValidateDocument()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateQuery(t *testing.T) {
	tests := []struct {
		name    string
		query   model.Query
		wantErr bool
	}{
		{
			"valid query",
			model.Query{
				Collection: "users",
				Limit:      10,
				Filters:    []model.Filter{{Field: "age", Op: ">", Value: 18}},
				OrderBy:    []model.Order{{Field: "age", Direction: "asc"}},
			},
			false,
		},
		{
			"invalid collection",
			model.Query{Collection: "users/alice"},
			true,
		},
		{
			"negative limit",
			model.Query{Collection: "users", Limit: -1},
			true,
		},
		{
			"limit too high",
			model.Query{Collection: "users", Limit: 1001},
			true,
		},
		{
			"empty filter field",
			model.Query{Collection: "users", Filters: []model.Filter{{Field: "", Op: ">", Value: 18}}},
			true,
		},
		{
			"empty filter op",
			model.Query{Collection: "users", Filters: []model.Filter{{Field: "age", Op: "", Value: 18}}},
			true,
		},
		{
			"empty orderby field",
			model.Query{Collection: "users", OrderBy: []model.Order{{Field: "", Direction: "asc"}}},
			true,
		},
		{
			"invalid orderby direction",
			model.Query{Collection: "users", OrderBy: []model.Order{{Field: "age", Direction: "up"}}},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateQuery(tt.query)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateReplicationPull(t *testing.T) {
	tests := []struct {
		name    string
		req     storage.ReplicationPullRequest
		wantErr bool
	}{
		{
			"valid request",
			storage.ReplicationPullRequest{Collection: "users", Limit: 100},
			false,
		},
		{
			"invalid collection",
			storage.ReplicationPullRequest{Collection: "users/alice"},
			true,
		},
		{
			"negative limit",
			storage.ReplicationPullRequest{Collection: "users", Limit: -1},
			true,
		},
		{
			"limit too high",
			storage.ReplicationPullRequest{Collection: "users", Limit: 1001},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReplicationPull(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateReplicationPush(t *testing.T) {
	tests := []struct {
		name    string
		req     storage.ReplicationPushRequest
		wantErr bool
	}{
		{
			"valid request",
			storage.ReplicationPushRequest{
				Collection: "users",
				Changes: []storage.ReplicationPushChange{
					{Doc: &storage.Document{Fullpath: "users/alice"}},
				},
			},
			false,
		},
		{
			"invalid collection",
			storage.ReplicationPushRequest{Collection: "users/alice"},
			true,
		},
		{
			"nil doc",
			storage.ReplicationPushRequest{
				Collection: "users",
				Changes: []storage.ReplicationPushChange{
					{Doc: nil},
				},
			},
			true,
		},
		{
			"invalid doc path syntax",
			storage.ReplicationPushRequest{
				Collection: "users",
				Changes: []storage.ReplicationPushChange{
					{Doc: &storage.Document{Fullpath: "users/alice!"}},
				},
			},
			true,
		},
		{
			"doc path mismatch collection",
			storage.ReplicationPushRequest{
				Collection: "users",
				Changes: []storage.ReplicationPushChange{
					{Doc: &storage.Document{Fullpath: "posts/post1"}},
				},
			},
			true,
		},
		{
			"doc path is not a document path",
			storage.ReplicationPushRequest{
				Collection: "users",
				Changes: []storage.ReplicationPushChange{
					{Doc: &storage.Document{Fullpath: "users/alice/posts"}},
				},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReplicationPush(tt.req)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
