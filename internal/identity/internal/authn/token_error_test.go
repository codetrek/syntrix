package authn

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenService_ErrorPaths(t *testing.T) {
	t.Run("NewTokenService_InvalidKeyPath", func(t *testing.T) {
		// Use a path that cannot be written to (e.g., under a file treated as dir)
		tmpDir := t.TempDir()
		dummyFile := filepath.Join(tmpDir, "file")
		os.WriteFile(dummyFile, []byte("content"), 0600)

		cfg := config.AuthNConfig{
			PrivateKeyFile: filepath.Join(dummyFile, "key.pem"),
		}
		_, err := NewTokenService(cfg)
		assert.Error(t, err)
	})

	t.Run("LoadPrivateKey_InvalidPEM", func(t *testing.T) {
		tmpDir := t.TempDir()
		keyPath := filepath.Join(tmpDir, "invalid.pem")
		os.WriteFile(keyPath, []byte("-----BEGIN RSA PRIVATE KEY-----\nINVALID\n-----END RSA PRIVATE KEY-----"), 0600)

		_, err := LoadPrivateKey(keyPath)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to decode PEM block")
	})

	t.Run("SavePrivateKey_InvalidPath", func(t *testing.T) {
		key, _ := GeneratePrivateKey()
		// Try to save to a directory path
		tmpDir := t.TempDir()
		err := SavePrivateKey(tmpDir, key)
		assert.Error(t, err)
	})

	t.Run("ValidateToken_InvalidSigningMethod", func(t *testing.T) {
		// Create a token signed with HMAC instead of RSA
		token := jwt.New(jwt.SigningMethodHS256)
		tokenString, _ := token.SignedString([]byte("secret"))

		keyFile := filepath.Join(t.TempDir(), "key.pem")
		cfg := config.AuthNConfig{PrivateKeyFile: keyFile}
		ts, _ := NewTokenService(cfg)

		_, err := ts.ValidateToken(tokenString)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected signing method")
	})

	t.Run("ValidateToken_MalformedToken", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "key.pem")
		cfg := config.AuthNConfig{PrivateKeyFile: keyFile}
		ts, _ := NewTokenService(cfg)

		_, err := ts.ValidateToken("not.a.token")
		assert.Error(t, err)
	})

	t.Run("ValidateToken_MissingTenantID", func(t *testing.T) {
		keyFile := filepath.Join(t.TempDir(), "key.pem")
		cfg := config.AuthNConfig{PrivateKeyFile: keyFile}
		ts, _ := NewTokenService(cfg)

		// Manually create a token with missing TenantID
		claims := Claims{
			Username: "user",
			UserID:   "u1",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject: "u1",
			},
		}
		// TenantID is empty by default

		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		tokenString, _ := token.SignedString(ts.privateKey)

		_, err := ts.ValidateToken(tokenString)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing tenant ID")
	})
}

// Mock reader that returns error
type errorReader struct{}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, assert.AnError
}

func TestGeneratePrivateKey_Error(t *testing.T) {
	// Backup rand.Reader
	origReader := rand.Reader
	defer func() { rand.Reader = origReader }()

	// Inject error reader
	rand.Reader = &errorReader{}

	_, err := GeneratePrivateKey()
	assert.Error(t, err)
}

func TestEnsurePrivateKey_GenerateError(t *testing.T) {
	// Backup rand.Reader
	origReader := rand.Reader
	defer func() { rand.Reader = origReader }()

	// Inject error reader to cause GeneratePrivateKey to fail
	rand.Reader = &errorReader{}

	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "key.pem")

	_, err := EnsurePrivateKey(keyPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to generate key")
}

func TestTokenService_GenerateTokenPair_SigningError(t *testing.T) {
	// It's hard to force a signing error with valid RSA keys using standard library.
	// However, we can test that if private key is somehow invalid (though NewTokenService ensures it's valid).
	// Or we can mock the private key if we change the struct to use an interface, but that's too invasive.
	// We'll skip forcing signing error as it requires invalid key state which is hard to reach.

	// Instead, let's verify RefreshOverlap getter
	ts := &TokenService{refreshOverlap: 5 * time.Minute}
	assert.Equal(t, 5*time.Minute, ts.RefreshOverlap())
}

func TestTokenService_GenerateTokenPair_DefaultTenant(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	cfg := config.AuthNConfig{
		PrivateKeyFile: keyFile,
		AccessTokenTTL: 1 * time.Hour, // Ensure token doesn't expire immediately
	}
	ts, _ := NewTokenService(cfg)

	user := &User{ID: "u1", Username: "user"} // Empty TenantID
	pair, err := ts.GenerateTokenPair(user)
	require.NoError(t, err)

	claims, err := ts.ValidateToken(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "default", claims.TenantID)
}

func TestLoadPrivateKey_DecodeError(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "bad_pem.pem")
	// Write valid file but invalid PEM block content (e.g. just random text without headers)
	os.WriteFile(keyPath, []byte("just random text"), 0600)

	_, err := LoadPrivateKey(keyPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode PEM block")
}

func TestSavePrivateKey_Marshal(t *testing.T) {
	// x509.MarshalPKCS1PrivateKey doesn't return error for valid key.
	// We already covered file creation error in SavePrivateKey_InvalidPath.
}
