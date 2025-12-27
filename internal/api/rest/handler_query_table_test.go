package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockQueryService)
			if tt.setupMock != nil {
				tt.setupMock(mockService)
			}

			server := createTestServer(mockService, nil, nil)

			req := httptest.NewRequest("POST", "/api/v1/query", bytes.NewReader([]byte(tt.body)))
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
