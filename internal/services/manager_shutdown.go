package services

import (
	"context"
	"io"
	"log/slog"

	"github.com/syntrixbase/syntrix/internal/server"
)

func (m *Manager) Shutdown(ctx context.Context) {
	// Close streamer client if using remote connection
	if m.streamerClient != nil {
		if closer, ok := m.streamerClient.(io.Closer); ok {
			slog.Info("Closing Streamer gRPC client...")
			if err := closer.Close(); err != nil {
				slog.Error("Error closing Streamer client", "error", err)
			}
		}
	}

	// Close storage providers if initialized
	if m.storageFactory != nil {
		defer func() {
			if err := m.storageFactory.Close(); err != nil {
				slog.Error("Error closing storage factory", "error", err)
			}
		}()
	}

	if m.streamerService != nil {
		if err := m.streamerService.Stop(ctx); err != nil {
			slog.Error("Error stopping Streamer service", "error", err)
		}
	}

	// GracefulStop waits for active RPCs, so close subscriptions before waiting
	// for transport shutdown. The Puller log remains open until its normal stop.
	if m.pullerGRPC != nil {
		m.pullerGRPC.Shutdown()
	}

	// Stop Unified Server Service
	if s := server.Default(); s != nil {
		slog.Info("Stopping Unified Server Service...")
		if err := s.Stop(ctx); err != nil {
			slog.Error("Error stopping Unified Server Service", "error", err)
		}
	}

	// Wait for background tasks (Trigger Watcher, Consumer)
	slog.Info("Waiting for background tasks to finish...")
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("Background tasks finished")
	case <-ctx.Done():
		slog.Warn("Timeout waiting for background tasks")
	}

	// Close pubsub provider (handles both NATS and memory implementations)
	if m.pubsubProvider != nil {
		slog.Info("Closing pubsub provider...")
		if err := m.pubsubProvider.Close(); err != nil {
			slog.Error("Error closing pubsub provider", "error", err)
		}
	}

	// Stop Change Stream Puller
	if m.pullerService != nil {
		slog.Info("Stopping Change Stream Puller...")
		if err := m.pullerService.Stop(ctx); err != nil {
			slog.Error("Error stopping Change Stream Puller", "error", err)
		}
	}

	// Stop Indexer Service
	if m.indexerService != nil {
		slog.Info("Stopping Indexer Service...")
		if err := m.indexerService.Stop(ctx); err != nil {
			slog.Error("Error stopping Indexer Service", "error", err)
		}
	}
}
