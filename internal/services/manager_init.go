package services

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/codetrek/syntrix/internal/api"
	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/csp"
	"github.com/codetrek/syntrix/internal/engine"
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/puller"
	"github.com/codetrek/syntrix/internal/server"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
	triggerengine "github.com/codetrek/syntrix/internal/trigger/engine"

	"github.com/nats-io/nats.go"
)

var triggerFactoryFactory = func(store storage.DocumentStore, nats *nats.Conn, auth identity.AuthN, opts ...triggerengine.FactoryOption) (triggerengine.TriggerFactory, error) {
	// Default options
	defaultOpts := []triggerengine.FactoryOption{triggerengine.WithStartFromNow(true)}
	return triggerengine.NewFactory(store, nats, auth, append(defaultOpts, opts...)...)
}
var storageFactoryFactory = func(ctx context.Context, cfg *config.Config) (storage.StorageFactory, error) {
	return storage.NewFactory(ctx, cfg)
}

func (m *Manager) Init(ctx context.Context) error {
	if err := m.initStorage(ctx); err != nil {
		return err
	}

	if err := m.initAuthService(ctx); err != nil {
		return err
	}

	// Initialize Unified Server Service
	// Note: We ignore port conflicts for now as we are transitioning.
	// Existing services will continue to run on their own ports.
	server.InitDefault(m.cfg.Server, nil)

	if m.opts.RunPuller {
		if err := m.initPullerService(ctx); err != nil {
			return err
		}
	}

	// Standalone mode: all services run in-process without HTTP inter-service communication
	if m.opts.Mode == ModeStandalone {
		return m.initStandalone(ctx)
	}

	// Distributed mode: services communicate via HTTP
	return m.initDistributed(ctx)
}

// initStandalone initializes services for standalone deployment mode.
// All services run in a single process without HTTP inter-service communication.
func (m *Manager) initStandalone(ctx context.Context) error {
	// Create CSP service for local access (no HTTP server)
	cspService := m.createCSPService()
	log.Println("Initialized CSP Service (local)")

	// Create query service using local CSP service (no HTTP server)
	queryService := m.createQueryService(cspService)

	// API server is the only HTTP server in standalone mode
	if err := m.initAPIServer(queryService); err != nil {
		return err
	}

	return nil
}

// initDistributed initializes services for distributed deployment mode.
// Services communicate via HTTP and can run on separate machines.
func (m *Manager) initDistributed(ctx context.Context) error {
	var queryService engine.Service

	if m.opts.RunQuery {
		// In distributed mode, create remote CSP client
		cspService := csp.NewClient(m.cfg.Query.CSPServiceURL)
		queryService = m.createQueryService(cspService)
		if !m.opts.ForceQueryClient {
			m.initQueryHTTPServer(queryService)
		}
	}

	if m.opts.RunAPI {
		if queryService == nil {
			queryService = engine.NewClient(m.cfg.Gateway.QueryServiceURL)
		}
		if err := m.initAPIServer(queryService); err != nil {
			return err
		}
	}

	if m.opts.RunCSP {
		m.initCSPServer()
	}

	if m.opts.RunTriggerEvaluator || m.opts.RunTriggerWorker {
		if err := m.initTriggerServices(); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) initStorage(ctx context.Context) error {
	if !(m.opts.RunQuery || m.opts.RunCSP || m.opts.RunTriggerEvaluator || m.opts.RunAPI || m.opts.RunTriggerWorker) {
		return nil
	}

	var err error
	m.storageFactory, err = storageFactoryFactory(ctx, m.cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize storage factory: %w", err)
	}

	m.docStore = m.storageFactory.Document()
	m.userStore = m.storageFactory.User()
	m.revocationStore = m.storageFactory.Revocation()

	log.Println("Connected to Storage successfully.")
	return nil
}

func (m *Manager) initAuthService(ctx context.Context) error {
	if !m.opts.RunAPI && !m.opts.RunTriggerWorker {
		return nil
	}

	var authErr error
	m.authService, authErr = identity.NewAuthN(m.cfg.Identity.AuthN, m.userStore, m.revocationStore)
	if authErr != nil {
		return fmt.Errorf("failed to create auth service: %w", authErr)
	}

	log.Println("Initialized Auth Service")
	return nil
}

// createQueryService creates a query engine service using the given CSP service.
// This separates service creation from HTTP server setup for standalone mode support.
func (m *Manager) createQueryService(cspService csp.Service) engine.Service {
	service := engine.NewServiceWithCSP(m.docStore, cspService)
	log.Println("Initialized Local Query Engine")
	return service
}

// initQueryHTTPServer creates an HTTP server for the query service.
// In standalone mode, this is not called since query service runs in-process.
func (m *Manager) initQueryHTTPServer(service engine.Service) {
	m.servers = append(m.servers, &http.Server{
		Addr:    listenAddr(m.opts.ListenHost, m.cfg.Query.Port),
		Handler: engine.NewHTTPHandler(service),
	})
	m.serverNames = append(m.serverNames, "Query Service")
}

func (m *Manager) initAPIServer(queryService engine.Service) error {
	var authzEngine identity.AuthZ

	if m.cfg.Identity.AuthZ.RulesFile != "" {
		var err error
		authzEngine, err = identity.NewAuthZ(m.cfg.Identity.AuthZ, queryService)
		if err != nil {
			return fmt.Errorf("failed to create authz engine: %w", err)
		}

		log.Printf("Loaded authorization rules from %s", m.cfg.Identity.AuthZ.RulesFile)
	}

	// Always initialize realtime server as part of gateway
	rtCfg := realtime.Config{
		AllowedOrigins: m.cfg.Gateway.Realtime.AllowedOrigins,
		AllowDevOrigin: m.cfg.Gateway.Realtime.AllowDevOrigin,
		EnableAuth:     m.cfg.Gateway.Realtime.EnableAuth,
	}
	m.rtServer = realtime.NewServer(queryService, m.cfg.Storage.Topology.Document.DataCollection, m.authService, rtCfg)

	apiServer := api.NewServer(queryService, m.authService, authzEngine, m.rtServer)
	m.servers = append(m.servers, &http.Server{
		Addr:    listenAddr(m.opts.ListenHost, m.cfg.Gateway.Port),
		Handler: apiServer,
	})
	m.serverNames = append(m.serverNames, "Unified Gateway")

	return nil
}

// createCSPService creates a CSP service for local access (standalone mode).
func (m *Manager) createCSPService() csp.Service {
	return csp.NewService(m.docStore)
}

// initCSPServer creates an HTTP server for the CSP service (distributed mode).
func (m *Manager) initCSPServer() {
	cspServer := csp.NewServer(m.docStore)
	m.servers = append(m.servers, &http.Server{
		Addr:    listenAddr(m.opts.ListenHost, m.cfg.CSP.Port),
		Handler: cspServer,
	})
	m.serverNames = append(m.serverNames, "CSP Service")
}

func listenAddr(host string, port int) string {
	if host == "" {
		return fmt.Sprintf(":%d", port)
	}

	return net.JoinHostPort(host, strconv.Itoa(port))
}

func (m *Manager) initTriggerServices() error {
	// Create NATS provider based on deployment mode
	if m.opts.Mode == ModeStandalone && m.cfg.Deployment.Standalone.EmbeddedNATS {
		m.natsProvider = trigger.NewEmbeddedNATSProvider(m.cfg.Deployment.Standalone.NATSDataDir)
	} else {
		m.natsProvider = trigger.NewRemoteNATSProvider(m.cfg.Trigger.NatsURL)
	}

	nc, err := m.natsProvider.Connect(context.Background())
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	factory, err := triggerFactoryFactory(m.docStore, nc, m.authService, triggerengine.WithStreamName(m.cfg.Trigger.StreamName))
	if err != nil {
		return fmt.Errorf("failed to create trigger factory: %w", err)
	}

	if m.opts.RunTriggerEvaluator {
		engine, err := factory.Engine()
		if err != nil {
			return fmt.Errorf("failed to create trigger engine: %w", err)
		}
		m.triggerService = engine

		rulesFile := m.cfg.Trigger.RulesFile
		if rulesFile != "" {
			triggers, err := trigger.LoadTriggersFromFile(rulesFile)
			if err != nil {
				log.Printf("[Warning] Failed to load trigger rules from %s: %v", rulesFile, err)
			} else {
				if err := m.triggerService.LoadTriggers(triggers); err != nil {
					return fmt.Errorf("failed to load triggers: %w", err)
				}
				log.Printf("Loaded %d triggers from %s", len(triggers), rulesFile)
			}
		}

		log.Println("Initialized Trigger Evaluator Service")
	}

	if m.opts.RunTriggerWorker {
		cons, err := factory.Consumer(m.cfg.Trigger.WorkerCount)
		if err != nil {
			return fmt.Errorf("failed to create trigger consumer: %w", err)
		}
		m.triggerConsumer = cons

		log.Println("Initialized Trigger Worker Service")
	}

	return nil
}

func (m *Manager) initPullerService(ctx context.Context) error {
	log.Println("Initializing Change Stream Puller Service...")

	// 1. Create Puller Service
	m.pullerService = puller.NewService(m.cfg.Puller, nil) // Logger is nil for now

	// 2. Add Backends
	for _, backendCfg := range m.cfg.Puller.Backends {
		client, dbName, err := m.storageFactory.GetMongoClient(backendCfg.Name)
		if err != nil {
			return fmt.Errorf("failed to get mongo client for backend %s: %w", backendCfg.Name, err)
		}

		if err := m.pullerService.AddBackend(backendCfg.Name, client, dbName, backendCfg); err != nil {
			return fmt.Errorf("failed to add backend %s: %w", backendCfg.Name, err)
		}
		log.Printf("- Added backend: %s (db: %s)", backendCfg.Name, dbName)
	}

	// 3. Initialize gRPC Server (if distributed mode)
	if m.opts.Mode == ModeDistributed {
		m.pullerGRPC = puller.NewGRPCServer(m.cfg.Puller.GRPC, m.pullerService, nil)
	}

	return nil
}
