package services

import (
	"context"
	"log"
)

func (m *Manager) Shutdown(ctx context.Context) {
	// Close storage providers if initialized
	if m.storageFactory != nil {
		defer func() {
			if err := m.storageFactory.Close(); err != nil {
				log.Printf("Error closing storage factory: %v", err)
			}
		}()
	}

	for i, srv := range m.servers {
		log.Printf("Stopping %s...", m.serverNames[i])
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down %s: %v", m.serverNames[i], err)
		}
	}

	// Wait for background tasks (Trigger Watcher, Consumer)
	log.Println("Waiting for background tasks to finish...")
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("Background tasks finished.")
	case <-ctx.Done():
		log.Println("Timeout waiting for background tasks.")
	}

	// Close NATS provider (handles both connection and any embedded server)
	if m.natsProvider != nil {
		log.Println("Closing NATS provider...")
		m.natsProvider.Close()
	}
}
