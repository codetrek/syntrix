package services

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/codetrek/syntrix/internal/api"
	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/auth"
	"github.com/codetrek/syntrix/internal/authz"
	"github.com/codetrek/syntrix/internal/csp"
	"github.com/codetrek/syntrix/internal/query"
	mongostore "github.com/codetrek/syntrix/internal/storage/mongo"
	"github.com/codetrek/syntrix/internal/trigger"

	"github.com/nats-io/nats.go"
	"go.mongodb.org/mongo-driver/mongo"
)

// natsConnector allows test injection to avoid real network.
var natsConnector = nats.Connect
var triggerPublisherFactory = trigger.NewEventPublisher
var triggerConsumerFactory = trigger.NewConsumer
var triggerEvaluatorFactory = trigger.NewEvaluator
var mongoBackendFactory = func(ctx context.Context, uri, dbName, dataColl, sysColl string, softDeleteRetention time.Duration) (storageBackend, error) {
	return mongostore.NewMongoBackend(ctx, uri, dbName, dataColl, sysColl, softDeleteRetention)
}

var authStorageFactory = func(db *mongo.Database) authStorage {
	return auth.NewStorage(db)
}

func (m *Manager) Init(ctx context.Context) error {
	if err := m.initStorage(ctx); err != nil {
		return err
	}

	if err := m.initTokenService(); err != nil {
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
	if !(m.opts.RunQuery || m.opts.RunCSP || m.opts.RunTriggerEvaluator || m.opts.RunAuth) {
		return nil
	}

	mongoURI := m.cfg.Storage.MongoURI
	dbName := m.cfg.Storage.DatabaseName
	dataColl := m.cfg.Storage.DataCollection
	sysColl := m.cfg.Storage.SysCollection

	var err error
	m.storageBackend, err = mongoBackendFactory(ctx, mongoURI, dbName, dataColl, sysColl, m.cfg.Storage.SoftDeleteRetention)
	if err != nil {
		return fmt.Errorf("failed to connect to storage backend: %w", err)
	}

	log.Println("Connected to MongoDB successfully.")
	return nil
}

func (m *Manager) initTokenService() error {
	if !(m.opts.RunAuth || m.opts.RunTriggerWorker) {
		return nil
	}

	keyFile := m.cfg.Auth.PrivateKeyFile
	if keyFile == "" {
		return fmt.Errorf("No token private cert file")
	}

	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		log.Printf("Generating new private key at %s", keyFile)
		var privateKey, err = auth.GeneratePrivateKey()
		if err != nil {
			return fmt.Errorf("failed to generate private key: %w", err)
		}

		if err := os.MkdirAll(filepath.Dir(keyFile), 0755); err != nil {
			return fmt.Errorf("failed to create directory for private key: %w", err)
		}

		if err := auth.SavePrivateKey(keyFile, privateKey); err != nil {
			return fmt.Errorf("failed to save private key: %w", err)
		}
	}

	log.Printf("Loading private key from %s", keyFile)
	var privateKey, err = auth.LoadPrivateKey(keyFile)
	if err != nil {
		return fmt.Errorf("failed to load private key: %w", err)
	}

	m.tokenService, err = auth.NewTokenService(
		privateKey,
		m.cfg.Auth.AccessTokenTTL,
		m.cfg.Auth.RefreshTokenTTL,
		m.cfg.Auth.AuthCodeTTL,
	)
	if err != nil {
		return fmt.Errorf("failed to create token service: %w", err)
	}

	return nil
}

func (m *Manager) initAuthService(ctx context.Context) error {
	if !m.opts.RunAuth {
		return nil
	}

	authStorage := authStorageFactory(m.storageBackend.DB())
	m.authService = auth.NewAuthService(authStorage, m.tokenService)

	if err := authStorage.EnsureIndexes(ctx); err != nil {
		log.Printf("Warning: failed to ensure auth indexes: %v", err)
	}

	log.Println("Initialized Auth Service")
	return nil
}

func (m *Manager) initQueryServices() query.Service {
	if !m.opts.RunQuery {
		return nil
	}

	engine := query.NewEngine(m.storageBackend, m.cfg.Query.CSPServiceURL)
	m.servers = append(m.servers, &http.Server{
		Addr:    fmt.Sprintf(":%d", m.cfg.Query.Port),
		Handler: query.NewServer(engine),
	})
	m.serverNames = append(m.serverNames, "Query Service")

	log.Println("Initialized Local Query Engine")
	return engine
}

func (m *Manager) initAPIServer(queryService query.Service) error {
	var authzEngine authz.Engine

	if m.cfg.Auth.RulesFile != "" {
		var err error
		authzEngine, err = authz.NewEngine(queryService)
		if err != nil {
			return fmt.Errorf("failed to create authz engine: %w", err)
		}

		if err := authzEngine.LoadRules(m.cfg.Auth.RulesFile); err != nil {
			log.Printf("Error: failed to load rules from %s: %v", m.cfg.Auth.RulesFile, err)
			return fmt.Errorf("failed to load authorization rules")
		}

		log.Printf("Loaded authorization rules from %s", m.cfg.Auth.RulesFile)
	}

	// Always initialize realtime server as part of gateway
	m.rtServer = realtime.NewServer(queryService, m.cfg.Storage.DataCollection)

	apiServer := api.NewServer(queryService, m.authService, authzEngine, m.rtServer)
	m.servers = append(m.servers, &http.Server{
		Addr:    fmt.Sprintf(":%d", m.cfg.Gateway.Port),
		Handler: apiServer,
	})
	m.serverNames = append(m.serverNames, "Unified Gateway")

	return nil
}

func (m *Manager) initCSPServer() {
	cspServer := csp.NewServer(m.storageBackend)
	m.servers = append(m.servers, &http.Server{
		Addr:    fmt.Sprintf(":%d", m.cfg.CSP.Port),
		Handler: cspServer,
	})
	m.serverNames = append(m.serverNames, "CSP Service")
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

	if m.opts.RunTriggerEvaluator {
		evaluator, err := triggerEvaluatorFactory()
		if err != nil {
			return fmt.Errorf("failed to create CEL evaluator: %w", err)
		}

		publisher, err := triggerPublisherFactory(nc)
		if err != nil {
			return fmt.Errorf("failed to create NATS publisher: %w", err)
		}

		m.triggerService = trigger.NewTriggerService(evaluator, publisher)

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

	if m.opts.RunTriggerWorker {
		worker := trigger.NewDeliveryWorker(m.tokenService)
		m.triggerConsumer, err = triggerConsumerFactory(nc, worker, m.cfg.Trigger.WorkerCount)
		if err != nil {
			return fmt.Errorf("failed to create trigger consumer: %w", err)
		}

		log.Println("Initialized Trigger Worker Service")
	}

	return nil
}
