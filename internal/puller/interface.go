// Package puller implements the Change Stream Puller service.
//
// The puller watches MongoDB change streams and distributes events to consumers
// via gRPC streaming. It provides:
//
//   - Multi-backend support: Watch multiple MongoDB databases
//   - Checkpoint persistence: Resume from where we left off after restart
//   - Event buffering: PebbleDB-backed buffer for durability and replay
//   - Catch-up coalescing: Merge events for the same document during catch-up
//   - Health monitoring: HTTP health endpoint with backend status
//   - Error recovery: Automatic reconnection with backoff
//
// # Usage
//
// For the local puller service (in-process):
//
//	svc := puller.NewService(cfg, logger)
//	svc.AddBackend("main", mongoClient, "mydb", backendCfg)
//	svc.Start(ctx)
//
// For a remote puller client (gRPC):
//
//	client, err := puller.NewClient("localhost:50051", logger)
//	events, err := client.Subscribe(ctx, "consumer-1", "")
//
// # Package Organization
//
// The package is organized into internal subpackages:
//   - core: Local puller service implementation
//   - client: gRPC client for remote puller
//   - checkpoint: Resume token persistence
//   - normalizer: Convert MongoDB events to normalized format
//   - buffer: PebbleDB event storage and coalescing
//   - health: Health check and bootstrap
//   - recovery: Error handling and gap detection
//   - grpc: gRPC server and subscriber management
package puller

import (
	"context"
	"log/slog"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/events"
	"github.com/codetrek/syntrix/internal/puller/internal/client"
	"github.com/codetrek/syntrix/internal/puller/internal/core"
	pullergrpc "github.com/codetrek/syntrix/internal/puller/internal/grpc"
	"github.com/codetrek/syntrix/internal/puller/internal/health"
	"go.mongodb.org/mongo-driver/mongo"
)

// Service defines the interface for the Puller service.
// Both the local Puller and the remote Client implement this interface.
type Service interface {
	// Subscribe subscribes to events from the puller.
	// The after parameter is the progress marker to resume from (empty for beginning).
	// Returns a channel of events that will be closed when the subscription ends.
	Subscribe(ctx context.Context, consumerID string, after string) (<-chan *events.NormalizedEvent, error)
}

// LocalService extends Service with methods only available for local (in-process) pullers.
type LocalService interface {
	Service

	// AddBackend adds a MongoDB backend to watch.
	AddBackend(name string, client *mongo.Client, dbName string, cfg config.PullerBackendConfig) error

	// Start starts watching all backends.
	Start(ctx context.Context) error

	// Stop stops all backends gracefully.
	Stop(ctx context.Context) error

	// BackendNames returns the names of all configured backends.
	BackendNames() []string

	// SetEventHandler sets the event handler for processing events.
	SetEventHandler(handler func(ctx context.Context, backendName string, event *events.NormalizedEvent) error)
}

// NewService creates a new local Puller service (in-process).
// Use this for the puller service that runs alongside storage.
func NewService(cfg config.PullerConfig, logger *slog.Logger) LocalService {
	return core.New(cfg, logger)
}

// NewClient creates a new remote Puller client (gRPC).
// Use this when the puller service is running remotely.
func NewClient(address string, logger *slog.Logger) (Service, error) {
	return client.New(address, logger)
}

// ============================================================================
// Health Check API
// ============================================================================

// Re-export types from internal packages for public API.
type (
	// HealthChecker provides health check functionality.
	HealthChecker = health.Checker

	// HealthReport is the full health report.
	HealthReport = health.Report

	// HealthStatus represents the health status of the puller.
	HealthStatus = health.Status

	// GRPCServer implements the PullerService gRPC interface.
	GRPCServer = pullergrpc.Server
)

// EventHandler is a function that handles events from the change stream.
type EventHandler = core.EventHandler

// Health status constants.
const (
	HealthOK        = health.StatusOK
	HealthDegraded  = health.StatusDegraded
	HealthUnhealthy = health.StatusUnhealthy
)

// Bootstrap mode constants.
const (
	BootstrapFromNow       = health.BootstrapFromNow
	BootstrapFromBeginning = health.BootstrapFromBeginning
)

// NewHealthChecker creates a new health checker.
func NewHealthChecker(logger *slog.Logger) *HealthChecker {
	return health.NewChecker(logger)
}

// StartHealthServer starts an HTTP health server.
func StartHealthServer(ctx context.Context, addr string, checker *HealthChecker) error {
	return health.StartServer(ctx, addr, checker)
}

// NewGRPCServer creates a new gRPC server for the puller service.
func NewGRPCServer(cfg config.PullerGRPCConfig, svc LocalService, logger *slog.Logger) *GRPCServer {
	return pullergrpc.NewServer(cfg, svc, logger)
}

// ============================================================================
// Deprecated type aliases and functions - for backward compatibility.
// These will be removed in a future release.
// ============================================================================

// Puller is a deprecated alias. Use NewService instead.
// Deprecated: Use NewService to get a LocalService interface.
type Puller = core.Puller

// NewPuller is deprecated. Use NewService instead.
// Deprecated: Use NewService to get a LocalService interface.
func NewPuller(cfg config.PullerConfig, logger *slog.Logger) *Puller {
	return core.New(cfg, logger)
}
