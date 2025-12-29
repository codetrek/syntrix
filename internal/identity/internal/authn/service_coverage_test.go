package authn

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSignUp_Coverage(t *testing.T) {
	type testCase struct {
		name        string
		req         SignupRequest
		mockSetup   func(*MockStorage)
		expectError bool
		errorMsg    string
	}

	tests := []testCase{
		{
			name: "Missing Tenant",
			req: SignupRequest{
				TenantID: "",
				Username: "user",
				Password: "password12345",
			},
			mockSetup:   nil,
			expectError: true,
			errorMsg:    ErrTenantRequired.Error(),
		},
		{
			name: "User Already Exists",
			req: SignupRequest{
				TenantID: "default",
				Username: "existing",
				Password: "password12345",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "existing").Return(&User{}, nil)
			},
			expectError: true,
			errorMsg:    "user already exists",
		},
		{
			name: "Storage Error (Check User)",
			req: SignupRequest{
				TenantID: "default",
				Username: "error",
				Password: "password12345",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "error").Return(nil, errors.New("db error"))
			},
			expectError: true,
			errorMsg:    "db error",
		},
		{
			name: "Password Too Short",
			req: SignupRequest{
				TenantID: "default",
				Username: "short",
				Password: "short",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "short").Return(nil, ErrUserNotFound)
			},
			expectError: true,
			errorMsg:    "password too short",
		},
		{
			name: "Storage Error (Create User)",
			req: SignupRequest{
				TenantID: "default",
				Username: "create_error",
				Password: "password12345",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "create_error").Return(nil, ErrUserNotFound)
				m.On("CreateUser", mock.Anything, "default", mock.Anything).Return(errors.New("create failed"))
			},
			expectError: true,
			errorMsg:    "create failed",
		},
		{
			name: "Admin Role Assignment",
			req: SignupRequest{
				TenantID: "default",
				Username: "syntrix",
				Password: "password12345",
			},
			mockSetup: func(m *MockStorage) {
				m.On("GetUserByUsername", mock.Anything, "default", "syntrix").Return(nil, ErrUserNotFound)
				m.On("CreateUser", mock.Anything, "default", mock.MatchedBy(func(u *User) bool {
					for _, r := range u.Roles {
						if r == "admin" {
							return true
						}
					}
					return false
				})).Return(nil)
			},
			expectError: false,
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

			tokenPair, err := authService.SignUp(context.Background(), tc.req)

			if tc.expectError {
				assert.Error(t, err)
				if tc.errorMsg != "" {
					assert.Contains(t, err.Error(), tc.errorMsg)
				}
				assert.Nil(t, tokenPair)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, tokenPair)
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestRefresh_Coverage(t *testing.T) {
	cfg := config.AuthNConfig{
		PrivateKeyFile:  filepath.Join(t.TempDir(), "key.pem"),
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
			name: "Revoked Token",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "revoked").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "revoked", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.RefreshToken
			},
			mockSetup: func(m *MockStorage) {
				m.On("IsRevoked", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
			},
			expectError: true,
			errorIs:     ErrInvalidToken,
		},
		{
			name: "Revocation Check Error",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "reverr").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "reverr", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.RefreshToken
			},
			mockSetup: func(m *MockStorage) {
				m.On("IsRevoked", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))
			},
			expectError: true,
		},
		{
			name: "User Not Found",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "missing").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "missing", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.RefreshToken
			},
			mockSetup: func(m *MockStorage) {
				m.On("IsRevoked", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
				m.On("GetUserByID", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("not found"))
			},
			expectError: true,
			errorIs:     ErrInvalidToken,
		},
		{
			name: "User Disabled",
			setupToken: func(t *testing.T, svc Service, m *MockStorage) string {
				m.On("GetUserByUsername", mock.Anything, "default", "disabled").Return(nil, ErrUserNotFound).Once()
				m.On("CreateUser", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				resp, err := svc.SignUp(context.Background(), SignupRequest{
					TenantID: "default", Username: "disabled", Password: "password12345",
				})
				require.NoError(t, err)
				return resp.RefreshToken
			},
			mockSetup: func(m *MockStorage) {
				m.On("IsRevoked", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
				m.On("GetUserByID", mock.Anything, mock.Anything, mock.Anything).Return(&User{
					ID: "user-id", TenantID: "default", Username: "disabled", Disabled: true,
				}, nil)
			},
			expectError: true,
			errorIs:     ErrAccountDisabled,
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
			}
		})
	}
}

func TestNewAuthService_Coverage(t *testing.T) {
	// Force invalid key path by using an existing file as the parent directory
	tmpDir := t.TempDir()
	parentFile := filepath.Join(tmpDir, "parent-file")
	require.NoError(t, os.WriteFile(parentFile, []byte("noop"), 0600))

	cfg := config.AuthNConfig{
		PrivateKeyFile: filepath.Join(parentFile, "key.pem"),
	}

	svc, err := NewAuthService(cfg, nil, nil)
	assert.Error(t, err)
	assert.Nil(t, svc)
}

func TestAuthService_Logout_Coverage(t *testing.T) {
	// Setup service with valid key (generate one)
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "test.pem")

	cfg := config.AuthNConfig{
		PrivateKeyFile: keyFile,
	}

	svc, err := NewAuthService(cfg, nil, nil)
	assert.NoError(t, err)

	// Case 1: Invalid token
	err = svc.Logout(context.Background(), "invalid-token")
	assert.Equal(t, ErrInvalidToken, err)
}
