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
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/codetrek/syntrix/internal/trigger/engine"

	"github.com/nats-io/nats.go"
)

// natsConnector allows test injection to avoid real network.
var natsConnector = nats.Connect
var triggerFactoryFactory = func(store storage.DocumentStore, nats *nats.Conn, auth identity.AuthN) (engine.TriggerFactory, error) {
	return engine.NewFactory(store, nats, auth, engine.WithStartFromNow(true))
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

	var queryService query.Service

	if m.opts.RunQuery {
		qs := m.initQueryServices()
		if !m.opts.ForceQueryClient {
			queryService = qs
		}
	}

	if m.opts.RunAPI {
		if queryService == nil {
			queryService = query.NewClient(m.cfg.Gateway.QueryServiceURL)
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

func (m *Manager) initQueryServices() query.Service {
	if !m.opts.RunQuery {
		return nil
	}

	// Use routed store which handles OpRead/OpWrite internally
	engine := query.NewEngine(m.docStore, m.cfg.Query.CSPServiceURL)
	m.servers = append(m.servers, &http.Server{
		Addr:    listenAddr(m.opts.ListenHost, m.cfg.Query.Port),
		Handler: query.NewServer(engine),
	})
	m.serverNames = append(m.serverNames, "Query Service")

	log.Println("Initialized Local Query Engine")
	return engine
}

func (m *Manager) initAPIServer(queryService query.Service) error {
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
	natsURL := m.cfg.Trigger.NatsURL
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	nc, err := natsConnector(natsURL)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	m.natsConn = nc

	factory, err := triggerFactoryFactory(m.docStore, nc, m.authService)
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
