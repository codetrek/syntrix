package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/syntrixbase/syntrix/internal/server"
)

// Start establishes durable source boundaries before starting event consumers.
// The caller cancels bgCtx and calls Shutdown if a synchronous startup fails.
func (m *Manager) Start(bgCtx context.Context) error {
	if err := bgCtx.Err(); err != nil {
		return err
	}
	if m.pullerService != nil {
		if err := m.pullerService.Start(bgCtx); err != nil {
			return fmt.Errorf("start Puller service: %w", err)
		}
	}
	// Start Unified Server Service
	if s := server.Default(); s != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			slog.Info("Starting Unified Server Service...")
			if err := s.Start(bgCtx); err != nil {
				slog.Error("Unified Server Service error", "error", err)
			}
		}()
	}

	// Distributed deployments may route collocated consumers through the unified
	// server, so launch it before their transport connection attempts.
	if m.streamerService != nil {
		if err := m.streamerService.Start(bgCtx); err != nil {
			return fmt.Errorf("start Streamer service: %w", err)
		}
	}
	if m.indexerService != nil {
		if err := m.indexerService.Start(bgCtx); err != nil {
			return fmt.Errorf("start Indexer service: %w", err)
		}
	}

	// Start Realtime Background Tasks with retry
	if m.rtServer != nil {
		go func() {
			// Give servers a moment to start
			time.Sleep(50 * time.Millisecond)

			maxRetries := 100
			for i := 0; i < maxRetries; i++ {
				// Check context before trying
				select {
				case <-bgCtx.Done():
					return
				default:
				}

				if err := m.rtServer.StartBackgroundTasks(bgCtx); err != nil {
					// Log every 10th attempt to reduce noise
					if (i+1)%10 == 0 {
						slog.Warn("Failed to start realtime background tasks", "attempt", i+1, "max_attempts", maxRetries, "error", err)
					}

					// Wait with context check
					select {
					case <-bgCtx.Done():
						return
					case <-time.After(50 * time.Millisecond):
						continue
					}
				}
				slog.Info("Realtime background tasks started successfully")
				return
			}
			slog.Error("Failed to start realtime background tasks after multiple attempts")
		}()
	}

	// Start Trigger Evaluator Service
	if m.triggerService != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			slog.Info("Starting Trigger Evaluator Service...")
			if err := m.triggerService.Start(bgCtx); err != nil {
				slog.Error("Failed to start trigger watcher", "error", err)
			}
		}()
	}

	// Start Trigger Consumer (Delivery Worker)
	if m.triggerConsumer != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			slog.Info("Starting Trigger Consumer...")
			if err := m.triggerConsumer.Start(bgCtx); err != nil {
				slog.Error("Trigger Consumer stopped with error", "error", err)
			}
		}()
	}

	// Start Deletion Worker
	if m.deletionWorker != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if err := m.deletionWorker.Start(bgCtx); err != nil {
				slog.Error("Failed to start Deletion Worker", "error", err)
			}
		}()
	}
	return nil
}
