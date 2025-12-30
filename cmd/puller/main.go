package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/puller"
)

func main() {
	// Setup logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("puller service starting")

	// Load configuration
	cfg := config.LoadConfig()

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Create puller instance
	p := puller.NewPuller(cfg.Puller, logger)

	// Create health checker
	health := puller.NewHealthChecker(logger)

	// Start health server in background
	go func() {
		healthAddr := ":8084" // Default health port
		if err := puller.StartHealthServer(ctx, healthAddr, health); err != nil {
			logger.Error("health server error", "error", err)
		}
	}()

	// Add backends from storage config
	for _, backendCfg := range cfg.Puller.Backends {
		storageCfg, ok := cfg.Storage.Backends[backendCfg.Name]
		if !ok {
			logger.Error("backend not found in storage config", "name", backendCfg.Name)
			os.Exit(1)
		}

		// For now, we don't actually connect to MongoDB here
		// This would be done when we have a real MongoDB client
		logger.Info("registered backend",
			"name", backendCfg.Name,
			"type", storageCfg.Type,
		)
		health.RegisterBackend(backendCfg.Name)
	}

	// Create gRPC server
	grpcServer := puller.NewGRPCServer(cfg.Puller.GRPC, p, logger)

	// Start gRPC server
	if err := grpcServer.Start(ctx); err != nil {
		logger.Error("failed to start gRPC server", "error", err)
		os.Exit(1)
	}

	// Note: In a real implementation, we would:
	// 1. Connect to MongoDB for each backend
	// 2. Start the puller with p.Start(ctx)
	// 3. Create event buffers
	// 4. Start the cleaner

	logger.Info("puller service started",
		"grpcAddress", cfg.Puller.GRPC.Address,
		"backends", len(cfg.Puller.Backends),
	)

	// Wait for shutdown signal
	sig := <-sigCh
	logger.Info("received shutdown signal", "signal", sig)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop gRPC server
	if err := grpcServer.Stop(shutdownCtx); err != nil {
		logger.Error("failed to stop gRPC server", "error", err)
	}

	// Stop puller
	if err := p.Stop(shutdownCtx); err != nil {
		logger.Error("failed to stop puller", "error", err)
	}

	// Cancel main context
	cancel()

	logger.Info("puller service stopped")
}
