package services

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/puller"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/nats-io/nats.go"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestManager_Init_Start_Shutdown_NoServices(t *testing.T) {
	cfg := config.LoadConfig()
	opts := Options{}
	mgr := NewManager(cfg, opts)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	assert.NoError(t, mgr.Init(ctx))

	bgCtx, bgCancel := context.WithCancel(context.Background())
	mgr.Start(bgCtx)
	bgCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	mgr.Shutdown(shutdownCtx)
}

type MockStorageFactory struct {
	mock.Mock
}

func (m *MockStorageFactory) Document() types.DocumentStore {
	return nil
}
func (m *MockStorageFactory) User() types.UserStore {
	return nil
}
func (m *MockStorageFactory) Revocation() types.TokenRevocationStore {
	return nil
}
func (m *MockStorageFactory) GetMongoClient(name string) (*mongo.Client, string, error) {
	return nil, "", nil
}
func (m *MockStorageFactory) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestManager_Shutdown_StorageError(t *testing.T) {
	// Override storage factory
	originalFactory := storageFactoryFactory
	defer func() { storageFactoryFactory = originalFactory }()

	mockFactory := new(MockStorageFactory)
	mockFactory.On("Close").Return(errors.New("storage close error"))

	storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
		return mockFactory, nil
	}

	cfg := config.LoadConfig()
	opts := Options{
		RunQuery: true,
	}
	mgr := NewManager(cfg, opts)

	ctx := context.Background()
	// Init to set storageFactory
	err := mgr.Init(ctx)
	require.NoError(t, err)

	// Shutdown
	mgr.Shutdown(ctx)

	mockFactory.AssertExpectations(t)
}

func TestManager_Shutdown_ServerShutdownError(t *testing.T) {
	// Create a manager with a server
	mgr := &Manager{
		serverNames: []string{"test-server"},
	}

	// Create a server
	srv := &http.Server{Addr: ":0"}
	mgr.servers = []*http.Server{srv}

	// Shutdown with cancelled context to trigger error (hopefully)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should not panic
	mgr.Shutdown(ctx)
}

func TestManager_Shutdown_Timeout(t *testing.T) {
	mgr := &Manager{}
	mgr.wg.Add(1) // Simulate a running task that never finishes

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Should timeout and log "Timeout waiting for background tasks."
	mgr.Shutdown(ctx)
}

type stubNATSProvider struct {
	closed bool
}

func (s *stubNATSProvider) Connect(ctx context.Context) (*nats.Conn, error) { return nil, nil }
func (s *stubNATSProvider) Close() error {
	s.closed = true
	return nil
}

func TestManager_Shutdown_PullerAndNATS(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	np := &stubNATSProvider{}
	pullerSvc := &stubPullerService{}
	mgr.natsProvider = np
	mgr.pullerService = pullerSvc
	mgr.pullerGRPC = puller.NewGRPCServer(config.PullerGRPCConfig{Address: "127.0.0.1:0"}, pullerSvc, nil)

	mgr.Shutdown(context.Background())

	assert.True(t, np.closed)
	assert.Equal(t, int32(1), pullerSvc.stopCount.Load())
}
