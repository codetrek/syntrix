package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAuthService for table-driven tests
type MockAuthService struct {
	mock.Mock
}

func (m *MockAuthService) Middleware(next http.Handler) http.Handler {
	return next
}
func (m *MockAuthService) MiddlewareOptional(next http.Handler) http.Handler {
	return next
}
func (m *MockAuthService) SignIn(ctx context.Context, req identity.LoginRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (m *MockAuthService) SignUp(ctx context.Context, req identity.SignupRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (m *MockAuthService) Refresh(ctx context.Context, req identity.RefreshRequest) (*identity.TokenPair, error) {
	return nil, nil
}
func (m *MockAuthService) ListUsers(ctx context.Context, limit int, offset int) ([]*identity.User, error) {
	return nil, nil
}
func (m *MockAuthService) UpdateUser(ctx context.Context, id string, roles []string, disabled bool) error {
	return nil
}
func (m *MockAuthService) Logout(ctx context.Context, refreshToken string) error {
	return nil
}
func (m *MockAuthService) GenerateSystemToken(serviceName string) (string, error) {
	return "", nil
}
func (m *MockAuthService) ValidateToken(tokenString string) (*identity.Claims, error) {
	args := m.Called(tokenString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*identity.Claims), args.Error(1)
}

func TestClientHandleMessage_TableDriven(t *testing.T) {
	tests := []struct {
		name            string
		msg             BaseMessage
		setupAuth       func(*MockAuthService)
		setupQuery      func(*MockQueryService)
		initialSubs     map[string]Subscription
		expectedType    string
		expectedPayload string // substring match
		checkClient     func(*testing.T, *Client)
	}{
		{
			name:         "Auth_Ack_NoAuthService",
			msg:          BaseMessage{Type: TypeAuth, ID: "req"},
			expectedType: TypeAuthAck,
		},
		{
			name: "Auth_Error_InvalidPayload",
			msg:  BaseMessage{Type: TypeAuth, ID: "req1", Payload: []byte(`invalid`)},
			setupAuth: func(m *MockAuthService) {
				// Auth service present but payload invalid
			},
			expectedType:    TypeError,
			expectedPayload: "invalid_auth",
		},
		{
			name: "Auth_Error_InvalidToken",
			msg: func() BaseMessage {
				payload, _ := json.Marshal(AuthPayload{Token: "bad"})
				return BaseMessage{Type: TypeAuth, ID: "req2", Payload: payload}
			}(),
			setupAuth: func(m *MockAuthService) {
				m.On("ValidateToken", "bad").Return(nil, assert.AnError)
			},
			expectedType:    TypeError,
			expectedPayload: "unauthorized",
		},
		{
			name: "Auth_Success",
			msg: func() BaseMessage {
				payload, _ := json.Marshal(AuthPayload{Token: "good"})
				return BaseMessage{Type: TypeAuth, ID: "req-ok", Payload: payload}
			}(),
			setupAuth: func(m *MockAuthService) {
				m.On("ValidateToken", "good").Return(&identity.Claims{TenantID: "default"}, nil)
			},
			expectedType: TypeAuthAck,
			checkClient: func(t *testing.T, c *Client) {
				assert.True(t, c.authenticated)
				assert.Equal(t, "default", c.tenant)
				assert.False(t, c.allowAllTenants)
			},
		},
		{
			name: "Auth_SystemRole",
			msg: func() BaseMessage {
				payload, _ := json.Marshal(AuthPayload{Token: "system"})
				return BaseMessage{Type: TypeAuth, ID: "req-sys", Payload: payload}
			}(),
			setupAuth: func(m *MockAuthService) {
				m.On("ValidateToken", "system").Return(&identity.Claims{TenantID: "default", Roles: []string{"system"}}, nil)
			},
			expectedType: TypeAuthAck,
			checkClient: func(t *testing.T, c *Client) {
				assert.True(t, c.authenticated)
				assert.True(t, c.allowAllTenants)
			},
		},
		{
			name: "Subscribe_Snapshot",
			msg: func() BaseMessage {
				payload := SubscribePayload{Query: model.Query{Collection: "users"}, IncludeData: true, SendSnapshot: true}
				b, _ := json.Marshal(payload)
				return BaseMessage{Type: TypeSubscribe, ID: "sub", Payload: b}
			}(),
			setupQuery: func(m *MockQueryService) {
				// Mock Pull for Snapshot
				// Note: The actual implementation calls Pull. We need to mock it.
				// However, MockQueryService in this package might need adjustment or we use the one from rest package?
				// Looking at client_message_test.go, it uses a local setupMockQuery helper.
				// Let's assume MockQueryService has Pull method mocked.
				// The previous test used:
				// m.On("Pull", mock.Anything, mock.Anything, mock.Anything).Return(&storage.ReplicationPullResponse{...}, nil).Maybe()
			},
			expectedType: TypeSubscribeAck,
			// Note: Snapshot message follows immediately. This test structure checks the first response.
			// We might need a special check for the second message.
		},
		{
			name: "Subscribe_CompileError",
			msg: func() BaseMessage {
				payload := SubscribePayload{Query: model.Query{Filters: []model.Filter{{Field: "age", Op: "!", Value: 1}}}}
				b, _ := json.Marshal(payload)
				return BaseMessage{Type: TypeSubscribe, ID: "sub-err", Payload: b}
			}(),
			expectedType: TypeError,
		},
		{
			name: "Subscribe_BadJSON",
			msg:  BaseMessage{Type: TypeSubscribe, ID: "sub-bad", Payload: []byte("{bad")},
			// Expect no response
			expectedType: "",
		},
		{
			name: "Unsubscribe",
			msg: func() BaseMessage {
				payload := UnsubscribePayload{ID: "sub"}
				b, _ := json.Marshal(payload)
				return BaseMessage{Type: TypeUnsubscribe, ID: "req1", Payload: b}
			}(),
			initialSubs:  map[string]Subscription{"sub": {}},
			expectedType: TypeUnsubscribeAck,
			checkClient: func(t *testing.T, c *Client) {
				_, ok := c.subscriptions["sub"]
				assert.False(t, ok)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewHub()
			qs := new(MockQueryService)
			// Default mock behavior for Pull if not specified
			qs.On("Pull", mock.Anything, mock.Anything, mock.Anything).Return(&storage.ReplicationPullResponse{}, nil).Maybe()

			if tt.setupQuery != nil {
				tt.setupQuery(qs)
			}

			var auth identity.AuthN
			if tt.setupAuth != nil {
				ma := &MockAuthService{}
				tt.setupAuth(ma)
				auth = ma
			}

			c := &Client{
				hub:           hub,
				queryService:  qs,
				send:          make(chan BaseMessage, 10), // Buffer to catch multiple messages
				subscriptions: make(map[string]Subscription),
				auth:          auth,
				authenticated: true, // Default to true for non-auth tests
			}

			if tt.initialSubs != nil {
				c.subscriptions = tt.initialSubs
			}

			// For Auth tests, we start unauthenticated
			if tt.msg.Type == TypeAuth {
				c.authenticated = false
			}

			c.handleMessage(tt.msg)

			if tt.expectedType == "" {
				select {
				case <-c.send:
					t.Fatal("expected no message")
				case <-time.After(20 * time.Millisecond):
					// OK
				}
			} else {
				select {
				case msg := <-c.send:
					assert.Equal(t, tt.expectedType, msg.Type)
					if tt.expectedPayload != "" {
						assert.Contains(t, string(msg.Payload), tt.expectedPayload)
					}
					if tt.name == "Subscribe_Snapshot" {
						// Check for second message
						select {
						case msg2 := <-c.send:
							assert.Equal(t, TypeSnapshot, msg2.Type)
						case <-time.After(100 * time.Millisecond):
							t.Fatal("expected snapshot message")
						}
					}
				case <-time.After(100 * time.Millisecond):
					t.Fatal("expected response message")
				}
			}

			if tt.checkClient != nil {
				tt.checkClient(t, c)
			}
		})
	}
}
