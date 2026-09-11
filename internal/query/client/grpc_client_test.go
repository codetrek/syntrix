package client

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	pb "github.com/syntrixbase/syntrix/api/gen/query/v1"
	grpctesting "github.com/syntrixbase/syntrix/api/gen/testing"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStatusToError(t *testing.T) {
	tests := []struct {
		name    string
		code    codes.Code
		wantErr error
	}{
		{"not found", codes.NotFound, model.ErrNotFound},
		{"precondition failed", codes.FailedPrecondition, model.ErrPreconditionFailed},
		{"already exists", codes.AlreadyExists, model.ErrExists},
		{"invalid argument", codes.InvalidArgument, model.ErrInvalidQuery},
		{"permission denied", codes.PermissionDenied, model.ErrPermissionDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := status.Error(tt.code, "test error")
			result := statusToError(grpcErr)
			if tt.code == codes.InvalidArgument {
				assert.ErrorIs(t, result, tt.wantErr)
				assert.Equal(t, "test error", result.Error())
			} else {
				assert.Equal(t, tt.wantErr, result)
			}
		})
	}

	t.Run("nil error", func(t *testing.T) {
		result := statusToError(nil)
		assert.Nil(t, result)
	})

	t.Run("unknown code", func(t *testing.T) {
		grpcErr := status.Error(codes.Internal, "internal error")
		result := statusToError(grpcErr)
		assert.Error(t, result)
		assert.Contains(t, result.Error(), "internal error")
	})

	t.Run("non-grpc error", func(t *testing.T) {
		result := statusToError(assert.AnError)
		assert.Equal(t, assert.AnError, result)
	})
}

func TestClient_Close(t *testing.T) {
	t.Run("close nil connection", func(t *testing.T) {
		client := &Client{}
		err := client.Close()
		assert.NoError(t, err)
	})
}

func TestNew_Success(t *testing.T) {
	// Test that New handles connection (gRPC connection is lazy)
	client, err := New("localhost:50051")
	assert.NoError(t, err)
	assert.NotNil(t, client)
	if client != nil {
		client.Close()
	}
}

func TestNew_Error(t *testing.T) {
	// Save original function and restore after test
	originalFunc := newClientFunc
	defer func() { newClientFunc = originalFunc }()

	// Mock the grpc.NewClient function to return an error
	newClientFunc = func(target string, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
		return nil, assert.AnError
	}

	client, err := New("localhost:50051")
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "failed to connect to query service")
}

func newTestClient(mockClient *grpctesting.MockQueryServiceClient) *Client {
	return &Client{
		client: mockClient,
	}
}

func TestClient_GetDocument(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("GetDocument", mock.Anything, mock.Anything).Return(&pb.GetDocumentResponse{
			Document: &pb.Document{
				Id:         "doc1",
				Collection: "users",
				Data:       []byte(`{"name":"Alice"}`),
			},
		}, nil)

		doc, err := client.GetDocument(context.Background(), "database1", "users/doc1")
		assert.NoError(t, err)
		assert.Equal(t, "doc1", doc["id"])
		assert.Equal(t, "Alice", doc["name"])
		mockClient.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("GetDocument", mock.Anything, mock.Anything).Return(nil, status.Error(codes.NotFound, "not found"))

		_, err := client.GetDocument(context.Background(), "database1", "users/doc1")
		assert.Error(t, err)
	})
}

func TestClient_CreateDocument(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("CreateDocument", mock.Anything, mock.Anything).Return(&pb.CreateDocumentResponse{}, nil)

		err := client.CreateDocument(context.Background(), "database1", model.Document{
			"id":   "doc1",
			"name": "Alice",
		})
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("CreateDocument", mock.Anything, mock.Anything).Return(nil, status.Error(codes.AlreadyExists, "already exists"))

		err := client.CreateDocument(context.Background(), "database1", model.Document{"id": "doc1"})
		assert.Error(t, err)
	})
}

func TestClient_ReplaceDocument(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("ReplaceDocument", mock.Anything, mock.Anything).Return(&pb.ReplaceDocumentResponse{
			Document: &pb.Document{
				Id:      "doc1",
				Version: 2,
				Data:    []byte(`{"name":"Bob"}`),
			},
		}, nil)

		doc, err := client.ReplaceDocument(context.Background(), "database1",
			model.Document{"id": "doc1", "name": "Bob"},
			model.Filters{{Field: "version", Op: "eq", Value: int64(1)}},
		)
		assert.NoError(t, err)
		assert.Equal(t, "doc1", doc["id"])
		assert.Equal(t, "Bob", doc["name"])
		mockClient.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("ReplaceDocument", mock.Anything, mock.Anything).Return(nil, status.Error(codes.FailedPrecondition, "version mismatch"))

		_, err := client.ReplaceDocument(context.Background(), "database1", model.Document{}, nil)
		assert.Error(t, err)
	})
}

func TestClient_PatchDocument(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("PatchDocument", mock.Anything, mock.Anything).Return(&pb.PatchDocumentResponse{
			Document: &pb.Document{
				Id:      "doc1",
				Version: 3,
				Data:    []byte(`{"name":"Charlie","age":30}`),
			},
		}, nil)

		doc, err := client.PatchDocument(context.Background(), "database1",
			model.Document{"age": 30},
			nil,
		)
		assert.NoError(t, err)
		assert.Equal(t, "doc1", doc["id"])
		assert.Equal(t, float64(30), doc["age"])
		mockClient.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("PatchDocument", mock.Anything, mock.Anything).Return(nil, status.Error(codes.NotFound, "not found"))

		_, err := client.PatchDocument(context.Background(), "database1", model.Document{}, nil)
		assert.Error(t, err)
	})
}

func TestClient_DeleteDocument(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("DeleteDocument", mock.Anything, mock.Anything).Return(&pb.DeleteDocumentResponse{}, nil)

		err := client.DeleteDocument(context.Background(), "database1", "users/doc1", nil)
		assert.NoError(t, err)
		mockClient.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("DeleteDocument", mock.Anything, mock.Anything).Return(nil, status.Error(codes.FailedPrecondition, "precondition failed"))

		err := client.DeleteDocument(context.Background(), "database1", "users/doc1", nil)
		assert.Error(t, err)
	})
}

func TestClient_ExecuteQuery(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("ExecuteQuery", mock.Anything, mock.Anything).Return(&pb.ExecuteQueryResponse{
			Documents: []*pb.Document{
				{Id: "doc1", Collection: "users", Data: []byte(`{"name":"Alice"}`)},
				{Id: "doc2", Collection: "users", Data: []byte(`{"name":"Bob"}`)},
			},
		}, nil)

		docs, err := client.ExecuteQuery(context.Background(), "database1", model.Query{
			Collection: "users",
			Limit:      10,
		})
		assert.NoError(t, err)
		assert.Len(t, docs, 2)
		assert.Equal(t, "doc1", docs[0]["id"])
		assert.Equal(t, "doc2", docs[1]["id"])
		mockClient.AssertExpectations(t)
	})

	t.Run("unsupported indexed operator", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)
		message := `invalid query: operator "in" is not supported by indexed queries`
		mockClient.On("ExecuteQuery", mock.Anything, mock.Anything).
			Return(nil, status.Error(codes.InvalidArgument, message)).Once()

		docs, err := client.ExecuteQuery(context.Background(), "database1", model.Query{
			Collection: "users",
			Filters:    model.Filters{{Field: "role", Op: model.OpIn, Value: []string{"admin"}}},
		})

		assert.Nil(t, docs)
		assert.ErrorIs(t, err, model.ErrInvalidQuery)
		assert.EqualError(t, err, message)
		mockClient.AssertExpectations(t)
	})

	t.Run("empty results", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("ExecuteQuery", mock.Anything, mock.Anything).Return(&pb.ExecuteQueryResponse{
			Documents: []*pb.Document{},
		}, nil)

		docs, err := client.ExecuteQuery(context.Background(), "database1", model.Query{
			Collection: "users",
		})
		assert.NoError(t, err)
		assert.Empty(t, docs)
	})

	t.Run("with filters and order", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("ExecuteQuery", mock.Anything, mock.Anything).Return(&pb.ExecuteQueryResponse{
			Documents: []*pb.Document{
				{Id: "doc1", Data: []byte(`{"age":25}`)},
			},
		}, nil)

		docs, err := client.ExecuteQuery(context.Background(), "database1", model.Query{
			Collection: "users",
			Filters: model.Filters{
				{Field: "age", Op: "gte", Value: 18},
			},
			OrderBy: []model.Order{
				{Field: "age", Direction: "asc"},
			},
			Limit:       100,
			ShowDeleted: true,
		})
		assert.NoError(t, err)
		assert.Len(t, docs, 1)
	})
}

func TestClient_Pull(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("Pull", mock.Anything, mock.Anything).Return(&pb.PullResponse{
			Documents: []*pb.Document{
				{Id: "doc1", Fullpath: "users/doc1", Version: 1, Data: []byte(`{"name":"Alice"}`)},
				{Id: "doc2", Fullpath: "users/doc2", Version: 2, Data: []byte(`{"name":"Bob"}`)},
			},
			Checkpoint: 123456789,
		}, nil)

		resp, err := client.Pull(context.Background(), "database1", storage.ReplicationPullRequest{
			Collection: "users",
			Checkpoint: 0,
			Limit:      100,
		})
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Documents, 2)
		assert.Equal(t, "doc1", resp.Documents[0].Id)
		assert.Equal(t, "doc2", resp.Documents[1].Id)
		assert.Equal(t, int64(123456789), resp.Checkpoint)
		mockClient.AssertExpectations(t)
	})

	t.Run("empty results", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("Pull", mock.Anything, mock.Anything).Return(&pb.PullResponse{
			Documents:  []*pb.Document{},
			Checkpoint: 987654321,
		}, nil)

		resp, err := client.Pull(context.Background(), "database1", storage.ReplicationPullRequest{
			Collection: "users",
			Checkpoint: 100,
		})
		assert.NoError(t, err)
		assert.Empty(t, resp.Documents)
		assert.Equal(t, int64(987654321), resp.Checkpoint)
	})
}

func TestClient_Push(t *testing.T) {
	t.Run("success no conflicts", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("Push", mock.Anything, mock.Anything).Return(&pb.PushResponse{
			Conflicts: []*pb.Document{},
		}, nil)

		baseVersion := int64(1)
		resp, err := client.Push(context.Background(), "database1", storage.ReplicationPushRequest{
			Collection: "users",
			Changes: []storage.ReplicationPushChange{
				{
					Doc: &storage.StoredDoc{
						Id:       "doc1",
						Fullpath: "users/doc1",
						Version:  2,
						Data:     map[string]interface{}{"name": "Alice Updated"},
					},
					BaseVersion: &baseVersion,
				},
			},
		})
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Empty(t, resp.Conflicts)
		mockClient.AssertExpectations(t)
	})

	t.Run("with conflicts", func(t *testing.T) {
		mockClient := grpctesting.NewMockQueryServiceClient()
		client := newTestClient(mockClient)

		mockClient.On("Push", mock.Anything, mock.Anything).Return(&pb.PushResponse{
			Conflicts: []*pb.Document{
				{Id: "doc1", Fullpath: "users/doc1", Version: 3, Data: []byte(`{"name":"Server Version"}`)},
			},
		}, nil)

		resp, err := client.Push(context.Background(), "database1", storage.ReplicationPushRequest{
			Collection: "users",
			Changes: []storage.ReplicationPushChange{
				{
					Doc: &storage.StoredDoc{
						Id:       "doc1",
						Fullpath: "users/doc1",
						Version:  2,
					},
					BaseVersion: nil, // No base version
				},
			},
		})
		assert.NoError(t, err)
		assert.Len(t, resp.Conflicts, 1)
		assert.Equal(t, "doc1", resp.Conflicts[0].Id)
	})
}
