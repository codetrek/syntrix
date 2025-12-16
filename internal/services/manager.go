package services

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"syntrix/internal/api"
	"syntrix/internal/config"
	"syntrix/internal/csp"
	"syntrix/internal/query"
	"syntrix/internal/realtime"
	"syntrix/internal/storage/mongo"
	"syntrix/internal/trigger"

	"github.com/nats-io/nats.go"
)

type Options struct {
	RunAPI              bool
	RunCSP              bool
	RunQuery            bool
	RunRealtime         bool
	RunTriggerEvaluator bool
	RunTriggerWorker    bool
}

type Manager struct {
	cfg             *config.Config
	opts            Options
	servers         []*http.Server
	serverNames     []string
	storageBackend  *mongo.MongoBackend
	rtServer        *realtime.Server
	triggerConsumer *trigger.Consumer
}

func NewManager(cfg *config.Config, opts Options) *Manager {
	return &Manager{
		cfg:  cfg,
		opts: opts,
	}
}

func (m *Manager) Init(ctx context.Context) error {
	// 1. Initialize Storage Backend (if needed)
	if m.opts.RunQuery || m.opts.RunCSP {
		// Use Query storage config as default, or CSP if Query is not running
		mongoURI := m.cfg.Query.Storage.MongoURI
		dbName := m.cfg.Query.Storage.DatabaseName
		if !m.opts.RunQuery && m.opts.RunCSP {
			mongoURI = m.cfg.CSP.Storage.MongoURI
			dbName = m.cfg.CSP.Storage.DatabaseName
		}

		var err error
		m.storageBackend, err = mongo.NewMongoBackend(ctx, mongoURI, dbName)
		if err != nil {
			return fmt.Errorf("failed to connect to storage backend: %w", err)
		}
		log.Println("Connected to MongoDB successfully.")
	}

	// 2. Initialize Query Service (Local Engine or Remote Client)
	var queryService query.Service
	var engine *query.Engine // Keep reference to engine if local

	if m.opts.RunQuery {
		engine = query.NewEngine(m.storageBackend, m.cfg.Query.CSPServiceURL)
		queryService = engine
		log.Println("Initialized Local Query Engine")
	}

	// 3. Initialize Services
	// Query Service (Internal API)
	if m.opts.RunQuery {
		queryServer := query.NewServer(engine)
		m.servers = append(m.servers, &http.Server{
			Addr:    fmt.Sprintf(":%d", m.cfg.Query.Port),
			Handler: queryServer,
		})
		m.serverNames = append(m.serverNames, "Query Service")
	}

	// API Gateway
	if m.opts.RunAPI {
		if queryService == nil {
			queryService = query.NewClient(m.cfg.API.QueryServiceURL)
		}

		apiServer := api.NewServer(queryService)
		m.servers = append(m.servers, &http.Server{
			Addr:    fmt.Sprintf(":%d", m.cfg.API.Port),
			Handler: apiServer,
		})
		m.serverNames = append(m.serverNames, "API Gateway")
	}

	// Realtime Gateway
	if m.opts.RunRealtime {
		if queryService == nil {
			queryService = query.NewClient(m.cfg.Realtime.QueryServiceURL)
		}

		m.rtServer = realtime.NewServer(queryService)
		m.servers = append(m.servers, &http.Server{
			Addr:    fmt.Sprintf(":%d", m.cfg.Realtime.Port),
			Handler: m.rtServer,
		})
		m.serverNames = append(m.serverNames, "Realtime Gateway")
	}

	// CSP Service
	if m.opts.RunCSP {
		cspServer := csp.NewServer(m.storageBackend)
		m.servers = append(m.servers, &http.Server{
			Addr:    fmt.Sprintf(":%d", m.cfg.CSP.Port),
			Handler: cspServer,
		})
		m.serverNames = append(m.serverNames, "CSP Service")
	}

	// Trigger Services (Evaluator & Worker)
	if m.opts.RunTriggerEvaluator || m.opts.RunTriggerWorker {
		// Connect to NATS
		natsURL := m.cfg.Trigger.NatsURL
		if natsURL == "" {
			natsURL = nats.DefaultURL
		}
		nc, err := nats.Connect(natsURL)
		if err != nil {
			return fmt.Errorf("failed to connect to NATS: %w", err)
		}

		// Initialize Evaluator Service
		if m.opts.RunTriggerEvaluator {
			evaluator, err := trigger.NewCELEvaluator()
			if err != nil {
				return fmt.Errorf("failed to create CEL evaluator: %w", err)
			}

			publisher, err := trigger.NewNatsPublisher(nc)
			if err != nil {
				return fmt.Errorf("failed to create NATS publisher: %w", err)
			}

			triggerService := trigger.NewTriggerService(evaluator, publisher)

			// TODO: Load triggers from storage
			// triggerService.LoadTriggers(...)

			// TODO: Hook up triggerService to Change Stream
			log.Println("Initialized Trigger Evaluator Service", triggerService)
		}

		// Initialize Worker Service
		if m.opts.RunTriggerWorker {
			worker := trigger.NewDeliveryWorker()
			m.triggerConsumer, err = trigger.NewConsumer(nc, worker)
			if err != nil {
				return fmt.Errorf("failed to create trigger consumer: %w", err)
			}
			log.Println("Initialized Trigger Worker Service")
		}
	}

	return nil
}

func (m *Manager) Start(bgCtx context.Context) {
	var wg sync.WaitGroup

	for i, srv := range m.servers {
		wg.Add(1)
		go func(s *http.Server, name string) {
			defer wg.Done()
			log.Printf("%s listening on %s", name, s.Addr)
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("%s error: %v", name, err)
			}
		}(srv, m.serverNames[i])
	}

	// Start Realtime Background Tasks with retry
	if m.opts.RunRealtime && m.rtServer != nil {
		go func() {
			// Give servers a moment to start
			time.Sleep(500 * time.Millisecond)

			maxRetries := 10
			for i := 0; i < maxRetries; i++ {
				if err := m.rtServer.StartBackgroundTasks(bgCtx); err != nil {
					log.Printf("Attempt %d/%d: Failed to start realtime background tasks: %v", i+1, maxRetries, err)
					time.Sleep(1 * time.Second)
					continue
				}
				log.Println("Realtime background tasks started successfully")
				return
			}
			log.Println("CRITICAL: Failed to start realtime background tasks after multiple attempts")
		}()
	}

	// Start Trigger Consumer
	if m.opts.RunTriggerWorker && m.triggerConsumer != nil {
		go func() {
			log.Println("Starting Trigger Consumer...")
			if err := m.triggerConsumer.Start(bgCtx); err != nil {
				log.Printf("Trigger Consumer stopped with error: %v", err)
			}
		}()
	}
}

func (m *Manager) Shutdown(ctx context.Context) {
	// Close storage backend if initialized
	if m.storageBackend != nil {
		defer func() {
			if err := m.storageBackend.Close(context.Background()); err != nil {
				log.Printf("Error closing storage backend: %v", err)
			}
		}()
	}

	for i, srv := range m.servers {
		log.Printf("Stopping %s...", m.serverNames[i])
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down %s: %v", m.serverNames[i], err)
		}
	}
}
