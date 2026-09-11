package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	pb "github.com/syntrixbase/syntrix/api/gen/query/v1"
	"github.com/syntrixbase/syntrix/internal/query"
	queryclient "github.com/syntrixbase/syntrix/internal/query/client"
	querygrpc "github.com/syntrixbase/syntrix/internal/query/grpc"
	"github.com/syntrixbase/syntrix/pkg/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestQueryHandler_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		setupMock      func(*MockQueryService)
		expectedStatus int
		expectedLen    int
	}{
		{
			name: "Success",
			body: `{"collection": "rooms/room-1/messages", "filters": [{"field": "name", "op": "==", "value": "Alice"}]}`,
			setupMock: func(m *MockQueryService) {
				docs := []model.Document{
					{"id": "msg-1", "collection": "rooms/room-1/messages", "name": "Alice", "version": int64(1)},
					{"id": "msg-2", "collection": "rooms/room-1/messages", "name": "Bob", "version": int64(1)},
				}
				m.On("ExecuteQuery", mock.Anything, "default", mock.AnythingOfType("model.Query")).Return(docs, nil)
			},
			expectedStatus: http.StatusOK,
			expectedLen:    2,
		},
		{
			name:           "BadJSON",
			body:           "{bad",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "ValidateError",
			body:           `{}`, // missing collection
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "EngineError",
			body: `{"collection": "rooms"}`,
			setupMock: func(m *MockQueryService) {
				q := model.Query{Collection: "rooms"}
				m.On("ExecuteQuery", mock.Anything, "default", q).Return(nil, assert.AnError)
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "Canceled",
			body: `{"collection": "rooms"}`,
			setupMock: func(m *MockQueryService) {
				m.On("ExecuteQuery", mock.Anything, "default", model.Query{Collection: "rooms"}).Return(nil, context.Canceled)
			},
			expectedStatus: 499,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockQueryService)
			if tt.setupMock != nil {
				tt.setupMock(mockService)
			}

			server := createTestServer(mockService, nil, nil)

			req := httptest.NewRequest("POST", "/api/v1/databases/default/query", bytes.NewReader([]byte(tt.body)))
			rr := httptest.NewRecorder()

			server.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.expectedStatus == http.StatusOK {
				var resp []map[string]interface{}
				json.Unmarshal(rr.Body.Bytes(), &resp)
				assert.Len(t, resp, tt.expectedLen)
			}

			mockService.AssertExpectations(t)
		})
	}
}

func TestQueryHandler_InvalidIndexedFilterTransportParity(t *testing.T) {
	for _, transport := range []string{"local", "grpc"} {
		t.Run(transport, func(t *testing.T) {
			service := new(MockQueryService)
			var engine query.Service = service
			if transport == "grpc" {
				listener := bufconn.Listen(1024 * 1024)
				server := grpc.NewServer()
				pb.RegisterQueryServiceServer(server, querygrpc.NewServer(service))
				serveDone := make(chan error, 1)
				go func() { serveDone <- server.Serve(listener) }()
				t.Cleanup(func() {
					server.Stop()
					_ = listener.Close()
					select {
					case err := <-serveDone:
						assert.NoError(t, err)
					case <-time.After(5 * time.Second):
						t.Error("query gRPC server did not stop")
					}
				})
				client, err := queryclient.NewWithOptions("passthrough:///query-test", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					return listener.DialContext(ctx)
				}))
				require.NoError(t, err)
				t.Cleanup(func() { assert.NoError(t, client.Close()) })
				engine = client
			}
			handler := createTestServer(engine, nil, nil)
			for _, op := range []model.FilterOp{model.OpNe, model.OpIn, model.OpContains} {
				t.Run(string(op), func(t *testing.T) {
					var value interface{} = "private-filter-value"
					if op == model.OpIn {
						value = []interface{}{"private-filter-value"}
					}
					q := model.Query{Collection: "users", Filters: model.Filters{
						{Field: "status", Op: model.OpEq, Value: "active"},
						{Field: "role", Op: op, Value: value},
					}}
					err := fmt.Errorf("%w: operator %q is not supported by indexed queries", model.ErrInvalidQuery, op)
					service.On("ExecuteQuery", mock.Anything, "default", mock.MatchedBy(func(actual model.Query) bool {
						return actual.Collection == q.Collection && assert.ObjectsAreEqual(actual.Filters, q.Filters) &&
							len(actual.OrderBy) == 0 && actual.Limit == 0 && actual.StartAfter == "" && !actual.ShowDeleted
					})).Return(nil, err).Once()
					body, marshalErr := json.Marshal(q)
					require.NoError(t, marshalErr)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/databases/default/query", bytes.NewReader(body))
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, req)
					require.Equal(t, http.StatusBadRequest, response.Code)
					var actual APIError
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &actual))
					assert.Equal(t, APIError{Code: ErrCodeBadRequest, Message: err.Error()}, actual)
					assert.NotContains(t, response.Body.String(), "private-filter-value")
				})
			}
			service.AssertExpectations(t)
		})
	}
}
