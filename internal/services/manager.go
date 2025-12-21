package services

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"syntrix/internal/api"
	"syntrix/internal/auth"
	"syntrix/internal/authz"
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
	RunAuth             bool
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
	authService     *auth.AuthService
	tokenService    *auth.TokenService
	rtServer        *realtime.Server
	triggerConsumer *trigger.Consumer
	triggerService  *trigger.TriggerService
	natsConn        *nats.Conn
	wg              sync.WaitGroup
}

func NewManager(cfg *config.Config, opts Options) *Manager {
	return &Manager{
		cfg:  cfg,
		opts: opts,
	}
}

func (m *Manager) TokenService() *auth.TokenService {
	return m.tokenService
}

func (m *Manager) Init(ctx context.Context) error {
	// 1. Initialize Storage Backend (if needed)
	if m.opts.RunQuery || m.opts.RunCSP || m.opts.RunTriggerEvaluator || m.opts.RunAuth {
		// Use Query storage config as default, or CSP if Query is not running
		mongoURI := m.cfg.Storage.MongoURI
		dbName := m.cfg.Storage.DatabaseName
		dataColl := m.cfg.Storage.DataCollection
		sysColl := m.cfg.Storage.SysCollection
		// If only Trigger Evaluator is running, we might want to use CSP config or Query config.
		// For simplicity, let's default to Query config (which is usually the main DB config).

		var err error
		m.storageBackend, err = mongo.NewMongoBackend(ctx, mongoURI, dbName, dataColl, sysColl, m.cfg.Storage.SoftDeleteRetention)
		if err != nil {
			return fmt.Errorf("failed to connect to storage backend: %w", err)
		}
		log.Println("Connected to MongoDB successfully.")
	}

	// Initialize Token Service if needed (Auth or Trigger Worker)
	if m.opts.RunAuth || m.opts.RunTriggerWorker {
		// Load or generate private key
		keyFile := m.cfg.Auth.PrivateKeyFile
		if keyFile == "" {
			keyFile = "private.pem"
		}

		var privateKey *rsa.PrivateKey
		if _, err := os.Stat(keyFile); os.IsNotExist(err) {
			log.Printf("Generating new private key at %s", keyFile)
			privateKey, err = auth.GeneratePrivateKey()
			if err != nil {
				return fmt.Errorf("failed to generate private key: %w", err)
			}

			// Ensure directory exists
			if err := os.MkdirAll(filepath.Dir(keyFile), 0755); err != nil {
				return fmt.Errorf("failed to create directory for private key: %w", err)
			}

			if err := auth.SavePrivateKey(keyFile, privateKey); err != nil {
				return fmt.Errorf("failed to save private key: %w", err)
			}
		} else {
			log.Printf("Loading private key from %s", keyFile)
			privateKey, err = auth.LoadPrivateKey(keyFile)
			if err != nil {
				return fmt.Errorf("failed to load private key: %w", err)
			}
		}

		var err error
		m.tokenService, err = auth.NewTokenService(
			privateKey,
			m.cfg.Auth.AccessTokenTTL,
			m.cfg.Auth.RefreshTokenTTL,
			m.cfg.Auth.AuthCodeTTL,
		)
		if err != nil {
			return fmt.Errorf("failed to create token service: %w", err)
		}
	}

	// Initialize Auth Service
	if m.opts.RunAuth {
		authStorage := auth.NewStorage(m.storageBackend.DB())
		m.authService = auth.NewAuthService(authStorage, m.tokenService)

		// Ensure indexes
		if err := authStorage.EnsureIndexes(ctx); err != nil {
			log.Printf("Warning: failed to ensure auth indexes: %v", err)
		}
		log.Println("Initialized Auth Service")
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

		// Initialize Authz Engine
		var authzEngine *authz.Engine
		if m.cfg.Auth.RulesFile != "" {
			var err error
			authzEngine, err = authz.NewEngine(queryService)
			if err != nil {
				return fmt.Errorf("failed to create authz engine: %w", err)
			}

			if err := authzEngine.LoadRules(m.cfg.Auth.RulesFile); err != nil {
				log.Printf("Error: failed to load rules from %s: %v", m.cfg.Auth.RulesFile, err)
				return fmt.Errorf("failed to load authorization rules")
			} else {
				log.Printf("Loaded authorization rules from %s", m.cfg.Auth.RulesFile)
			}
		}

		apiServer := api.NewServer(queryService, m.authService, authzEngine)
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

		m.rtServer = realtime.NewServer(queryService, m.cfg.Storage.DataCollection)
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
		m.natsConn = nc

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

			m.triggerService = trigger.NewTriggerService(evaluator, publisher)

			// Load triggers from file
			rulesFile := m.cfg.Trigger.RulesFile
			if rulesFile != "" {
				triggers, err := trigger.LoadTriggersFromFile(rulesFile)
				if err != nil {
					log.Printf("[Warning] Failed to load trigger rules from %s: %v", rulesFile, err)
				} else {
					m.triggerService.LoadTriggers(triggers)
					log.Printf("Loaded %d triggers from %s", len(triggers), rulesFile)
				}
			}

			log.Println("Initialized Trigger Evaluator Service")
		}

		// Initialize Worker Service
		if m.opts.RunTriggerWorker {
			worker := trigger.NewDeliveryWorker(m.tokenService)
			m.triggerConsumer, err = trigger.NewConsumer(nc, worker, m.cfg.Trigger.WorkerCount)
			if err != nil {
				return fmt.Errorf("failed to create trigger consumer: %w", err)
			}
			log.Println("Initialized Trigger Worker Service")
		}
	}

	return nil
}

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

	// Start Realtime Background Tasks with retry
	if m.opts.RunRealtime {
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
			if err := m.triggerService.Watch(bgCtx, m.storageBackend); err != nil {
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

	// Close NATS connection
	if m.natsConn != nil {
		log.Println("Closing NATS connection...")
		m.natsConn.Close()
	}
}
