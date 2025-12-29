package authn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSignUp_TableDriven(t *testing.T) {
	type testCase struct {
		name        string
		req         SignupRequest
		mockSetup   func(*MockStorage)
		expectError bool
	}

	tests := []testCase{
		{
			name: "Success",
			req: SignupRequest{
				TenantID: "default",
				Username: "newuser",
				Password: "password12345",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "newuser").Return(nil, ErrUserNotFound)
				m.On("CreateUser", mock.Anything, "default", mock.Anything).Return(nil)
			},
			expectError: false,
		},
		// Add more cases: UserExists, StorageError, etc.
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			cfg := config.AuthNConfig{
				PrivateKeyFile:  getTestKeyPath(t),
				AccessTokenTTL:  15 * time.Minute,
				RefreshTokenTTL: 7 * 24 * time.Hour,
				AuthCodeTTL:     2 * time.Minute,
			}
			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)
			authService := svc.(*AuthService)

			if tc.mockSetup != nil {
				tc.mockSetup(mockStorage)
			}

			tokenPair, err := authService.SignUp(context.Background(), tc.req)

			if tc.expectError {
				assert.Error(t, err)
				assert.Nil(t, tokenPair)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, tokenPair)
				assert.NotEmpty(t, tokenPair.AccessToken)
				assert.NotEmpty(t, tokenPair.RefreshToken)
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestSignIn_TableDriven(t *testing.T) {
	type testCase struct {
		name        string
		req         LoginRequest
		mockSetup   func(*MockStorage)
		expectError bool
		errorIs     error
	}

	tests := []testCase{
		{
			name: "Success",
			req: LoginRequest{
				TenantID: "default",
				Username: "existinguser",
				Password: "password123",
			},
			mockSetup: func(m *MockStorage) {
				hash, algo, _ := HashPassword("password123")
				user := &User{
					ID:           "user-id",
					TenantID:     "default",
					Username:     "existinguser",
					PasswordHash: hash,
					PasswordAlgo: algo,
				}
				m.On("GetUserByUsername", mock.Anything, "default", "existinguser").Return(user, nil)
				m.On("UpdateUserLoginStats", mock.Anything, "default", "user-id", mock.Anything, 0, mock.Anything).Return(nil)
			},
			expectError: false,
		},
		{
			name: "User Not Found",
			req: LoginRequest{
				TenantID: "default",
				Username: "nonexistent",
				Password: "password12345",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "nonexistent").Return(nil, ErrUserNotFound)
			},
			expectError: true,
			errorIs:     ErrUserNotFound,
		},
		{
			name: "Wrong Password",
			req: LoginRequest{
				TenantID: "default",
				Username: "existinguser",
				Password: "wrongpassword",
			},
			mockSetup: func(m *MockStorage) {
				hash, algo, _ := HashPassword("password123")
				user := &User{
					ID:           "user-id",
					TenantID:     "default",
					Username:     "existinguser",
					PasswordHash: hash,
					PasswordAlgo: algo,
				}
				m.On("GetUserByUsername", mock.Anything, "default", "existinguser").Return(user, nil)
				// Expect login stats update (failed attempt)
				m.On("UpdateUserLoginStats", mock.Anything, "default", "user-id", mock.Anything, 1, mock.Anything).Return(nil)
			},
			expectError: true,
			errorIs:     ErrInvalidCredentials,
		},
		{
			name: "Wrong Password Lockout",
			req: LoginRequest{
				TenantID: "default",
				Username: "existinguser",
				Password: "wrongpassword",
			},
			mockSetup: func(m *MockStorage) {
				hash, algo, _ := HashPassword("password123")
				user := &User{
					ID:            "user-id",
					TenantID:      "default",
					Username:      "existinguser",
					PasswordHash:  hash,
					PasswordAlgo:  algo,
					LoginAttempts: 9,
				}
				m.On("GetUserByUsername", mock.Anything, "default", "existinguser").Return(user, nil)
				// Expect login stats update (failed attempt, lockout)
				m.On("UpdateUserLoginStats", mock.Anything, "default", "user-id", mock.Anything, 10, mock.MatchedBy(func(lockoutUntil time.Time) bool {
					return lockoutUntil.After(time.Now())
				})).Return(nil)
			},
			expectError: true,
			errorIs:     ErrInvalidCredentials,
		},
		{
			name: "Locked Out",
			req: LoginRequest{
				TenantID: "default",
				Username: "locked",
				Password: "password123",
			},
			mockSetup: func(m *MockStorage) {
				user := &User{
					ID:           "user-id",
					TenantID:     "default",
					Username:     "locked",
					LockoutUntil: time.Now().Add(time.Hour),
				}
				m.On("GetUserByUsername", mock.Anything, "default", "locked").Return(user, nil)
			},
			expectError: true,
			errorIs:     ErrAccountLocked,
		},
		{
			name: "Disabled",
			req: LoginRequest{
				TenantID: "default",
				Username: "disabled",
				Password: "password123",
			},
			mockSetup: func(m *MockStorage) {
				hash, algo, _ := HashPassword("password123")
				user := &User{
					ID:           "user-id",
					TenantID:     "default",
					Username:     "disabled",
					PasswordHash: hash,
					PasswordAlgo: algo,
					Disabled:     true,
				}
				m.On("GetUserByUsername", mock.Anything, "default", "disabled").Return(user, nil)
			},
			expectError: true,
			errorIs:     ErrAccountDisabled,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			cfg := config.AuthNConfig{
				PrivateKeyFile:  getTestKeyPath(t),
				AccessTokenTTL:  15 * time.Minute,
				RefreshTokenTTL: 7 * 24 * time.Hour,
				AuthCodeTTL:     2 * time.Minute,
			}
			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)
			authService := svc.(*AuthService)

			if tc.mockSetup != nil {
				tc.mockSetup(mockStorage)
			}

			tokenPair, err := authService.SignIn(context.Background(), tc.req)

			if tc.expectError {
				assert.Error(t, err)
				assert.Nil(t, tokenPair)
				if tc.errorIs != nil {
					assert.Equal(t, tc.errorIs, err)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, tokenPair)
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestRefresh_TableDriven(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile:  getTestKeyPath(t),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	}

	tests := []struct {
		name        string
		setupToken  func(t *testing.T, svc Service, m *MockStorage) string
		mockSetup   func(*MockStorage)
		expectError bool
		errorIs     error
	}{
		{
			name: "Success",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				// Mock SignUp to get a token
				m.On("GetUserByUsername", mock.Anything, "default", "refreshuser").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "refreshuser", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.RefreshToken
			},
			mockSetup: func(m *MockStorage) {
				m.On("IsRevoked", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
				m.On("GetUserByID", mock.Anything, mock.Anything, mock.Anything).Return(&User{
					ID: "user-id", TenantID: "default", Username: "refreshuser", Disabled: false,
				}, nil)
				m.On("RevokeToken", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			},
			expectError: false,
		},
		{
			name: "Invalid Token",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				return "invalid-token"
			},
			mockSetup:   func(m *MockStorage) {},
			expectError: true,
			errorIs:     ErrInvalidToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)

			token := tt.setupToken(t, svc, mockStorage)

			if tt.mockSetup != nil {
				tt.mockSetup(mockStorage)
			}

			resp, err := svc.Refresh(context.Background(), RefreshRequest{RefreshToken: token})

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorIs != nil {
					assert.ErrorIs(t, err, tt.errorIs)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				assert.NotEmpty(t, resp.AccessToken)
			}
		})
	}
}

func TestLogout_TableDriven(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile:  getTestKeyPath(t),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	}

	tests := []struct {
		name        string
		setupToken  func(t *testing.T, svc Service, m *MockStorage) string
		mockSetup   func(*MockStorage)
		expectError bool
	}{
		{
			name: "Success",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "logoutuser").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "logoutuser", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.AccessToken
			},
			mockSetup: func(m *MockStorage) {
				m.On("RevokeTokenImmediate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)

			token := tt.setupToken(t, svc, mockStorage)

			if tt.mockSetup != nil {
				tt.mockSetup(mockStorage)
			}

			err = svc.Logout(context.Background(), token)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMiddleware_TableDriven(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile:  getTestKeyPath(t),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	}

	tests := []struct {
		name           string
		setupAuth      func(t *testing.T, svc Service, m *MockStorage) string
		headerValue    string
		expectedStatus int
	}{
		{
			name: "Valid Token",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "mwuser").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "mwuser", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.AccessToken
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "Missing Header",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				return ""
			},
			headerValue:    "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "Invalid Format",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				return ""
			},
			headerValue:    "InvalidFormat",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			// Mock IsRevoked for Middleware check
			// Note: Middleware calls ValidateToken which checks signature.
			// It doesn't call IsRevoked unless we add that check in Middleware (which is not in the code I read).
			// Wait, let me check Middleware code again.
			// It calls s.tokenService.ValidateToken(tokenString).
			// It does NOT call IsRevoked.

			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)

			token := tt.setupAuth(t, svc, mockStorage)

			handler := svc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			if tt.headerValue != "" {
				req.Header.Set("Authorization", tt.headerValue)
			} else if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}

func TestListUsers_TableDriven(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
	}

	tests := []struct {
		name        string
		tenantID    string
		mockSetup   func(*MockStorage)
		expectError bool
	}{
		{
			name:     "Success",
			tenantID: "default",
			mockSetup: func(m *MockStorage) {
				m.On("ListUsers", mock.Anything, "default", 10, 0).Return([]*User{}, nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			if tt.mockSetup != nil {
				tt.mockSetup(mockStorage)
			}

			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)

			// Inject tenant into context
			ctx := context.WithValue(context.Background(), ContextKeyTenant, tt.tenantID)
			_, err = svc.ListUsers(ctx, 10, 0) // limit, offset

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestUpdateUser_TableDriven(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile: getTestKeyPath(t),
	}

	tests := []struct {
		name        string
		tenantID    string
		userID      string
		roles       []string
		disabled    bool
		mockSetup   func(*MockStorage)
		expectError bool
	}{
		{
			name:     "Success",
			tenantID: "default",
			userID:   "user-id",
			roles:    []string{"admin"},
			disabled: false,
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByID", mock.Anything, "default", "user-id").Return(&User{ID: "user-id", TenantID: "default"}, nil)
				m.On("UpdateUser", mock.Anything, "default", mock.Anything).Return(nil)
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			if tt.mockSetup != nil {
				tt.mockSetup(mockStorage)
			}

			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)

			ctx := context.WithValue(context.Background(), ContextKeyTenant, tt.tenantID)
			err = svc.UpdateUser(ctx, tt.userID, tt.roles, tt.disabled)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMiddlewareOptional_TableDriven(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile:  getTestKeyPath(t),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
	}

	tests := []struct {
		name           string
		setupAuth      func(t *testing.T, svc Service, m *MockStorage) string
		headerValue    string
		expectedStatus int
	}{
		{
			name: "Valid Token",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "optuser").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "optuser", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.AccessToken
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "Missing Header",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				return ""
			},
			headerValue:    "",
			expectedStatus: http.StatusOK, // Should pass through
		},
		{
			name: "Invalid Format",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				return ""
			},
			headerValue:    "InvalidFormat",
			expectedStatus: http.StatusUnauthorized, // If header is present, it must be valid
		},
		{
			name: "Invalid Token",
			setupAuth: func(t *testing.T, svc Service, m *MockStorage) string {
				return "invalid-token"
			},
			headerValue:    "Bearer invalid-token",
			expectedStatus: http.StatusUnauthorized, // If header is present, it must be valid
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStorage := new(MockStorage)
			svc, err := NewAuthService(cfg, mockStorage, mockStorage)
			require.NoError(t, err)

			token := tt.setupAuth(t, svc, mockStorage)

			handler := svc.MiddlewareOptional(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/", nil)
			if tt.headerValue != "" {
				req.Header.Set("Authorization", tt.headerValue)
			} else if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
		})
	}
}
