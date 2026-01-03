package services

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/codetrek/syntrix/internal/server"
)

func (m *Manager) Start(bgCtx context.Context) {
	for i, srv := range m.servers {
		m.wg.Add(1)
		go func(s *http.Server, name string) {
			defer m.wg.Done()
			log.Printf("%s listening on %s", name, s.Addr)
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("%s error: %v", name, err)
			}
		}(srv, m.serverNames[i])
	}

	// Start Unified Server Service
	if s := server.Default(); s != nil {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			log.Println("Starting Unified Server Service...")
			if err := s.Start(bgCtx); err != nil {
				log.Printf("Unified Server Service error: %v", err)
			}
		}()
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
					if maxRetries%10 == 0 {
						log.Printf("Attempt %d/%d: Failed to start realtime background tasks: %v", i+1, maxRetries, err)
					}

					// Wait with context check
					select {
					case <-bgCtx.Done():
						return
					case <-time.After(50 * time.Millisecond):
						continue
					}
				}
				log.Println("Realtime background tasks started successfully")
				return
			}
			log.Println("CRITICAL: Failed to start realtime background tasks after multiple attempts")
		}()
	}

	// Start Trigger Evaluator (Change Stream Watcher)
	if m.opts.RunTriggerEvaluator {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if err := m.triggerService.Start(bgCtx); err != nil {
				log.Printf("Failed to start trigger watcher: %v", err)
			}
		}()
	}

	// Start Trigger Consumer
	if m.opts.RunTriggerWorker {
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			log.Println("Starting Trigger Consumer...")
			if err := m.triggerConsumer.Start(bgCtx); err != nil {
				log.Printf("Trigger Consumer stopped with error: %v", err)
			}
		}()
	}

	// Start Change Stream Puller
	if m.opts.RunPuller {
		log.Println("Starting Change Stream Puller...")
		if err := m.pullerService.Start(bgCtx); err != nil {
			log.Printf("Failed to start Change Stream Puller: %v", err)
		}

		// Start gRPC Server if configured
		if m.pullerGRPC != nil {
			log.Println("Starting Puller gRPC Server...")
			if err := m.pullerGRPC.Start(bgCtx); err != nil {
				log.Printf("Failed to start Puller gRPC Server: %v", err)
			}
		}
	}
}
