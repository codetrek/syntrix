package puller

import (
	"context"
	"log/slog"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
)

func TestNewService(t *testing.T) {
	t.Parallel()
	cfg := config.PullerConfig{}

	svc := NewService(cfg, nil)
	if svc == nil {
		t.Fatal("NewService() returned nil")
	}

	// Verify it returns a LocalService
	localSvc, ok := svc.(LocalService)
	if !ok {
		t.Error("NewService() should return LocalService")
	}

	// Verify BackendNames returns empty slice initially
	names := localSvc.BackendNames()
	if len(names) != 0 {
		t.Errorf("BackendNames() = %v, want empty", names)
	}
}

func TestNewService_WithLogger(t *testing.T) {
	t.Parallel()
	cfg := config.PullerConfig{}
	logger := slog.Default()

	svc := NewService(cfg, logger)
	if svc == nil {
		t.Fatal("NewService() with logger returned nil")
	}
}

func TestNewHealthChecker(t *testing.T) {
	t.Parallel()
	checker := NewHealthChecker(nil)
	if checker == nil {
		t.Fatal("NewHealthChecker() returned nil")
	}
}

func TestNewHealthChecker_WithLogger(t *testing.T) {
	t.Parallel()
	logger := slog.Default()
	checker := NewHealthChecker(logger)
	if checker == nil {
		t.Fatal("NewHealthChecker() with logger returned nil")
	}
}

func TestNewGRPCServer(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{
		Address:        ":50051",
		MaxConnections: 100,
	}
	svc := NewService(config.PullerConfig{}, nil)

	server := NewGRPCServer(cfg, svc, nil)
	if server == nil {
		t.Fatal("NewGRPCServer() returned nil")
	}
}

func TestNewGRPCServer_WithLogger(t *testing.T) {
	t.Parallel()
	cfg := config.PullerGRPCConfig{
		Address:        ":50051",
		MaxConnections: 100,
	}
	svc := NewService(config.PullerConfig{}, nil)
	logger := slog.Default()

	server := NewGRPCServer(cfg, svc, logger)
	if server == nil {
		t.Fatal("NewGRPCServer() with logger returned nil")
	}
}

func TestHealthStatusConstants(t *testing.T) {
	// Just verify the constants are exported correctly
	if HealthOK == "" {
		t.Error("HealthOK should not be empty")
	}
	if HealthDegraded == "" {
		t.Error("HealthDegraded should not be empty")
	}
	if HealthUnhealthy == "" {
		t.Error("HealthUnhealthy should not be empty")
	}
}

func TestBootstrapModeConstants(t *testing.T) {
	// Just verify the constants are exported correctly
	if BootstrapFromNow == "" {
		t.Error("BootstrapFromNow should not be empty")
	}
	if BootstrapFromBeginning == "" {
		t.Error("BootstrapFromBeginning should not be empty")
	}
}

func TestNewClient(t *testing.T) {
	c, err := NewClient("localhost:50051", nil)
	if err != nil {
		t.Errorf("NewClient failed: %v", err)
	}
	if c == nil {
		t.Error("NewClient returned nil")
	}
}

func TestStartHealthServer(t *testing.T) {
	hc := NewHealthChecker(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = StartHealthServer(ctx, ":0", hc)
	}()
}
