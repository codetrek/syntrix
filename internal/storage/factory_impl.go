package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage/internal/mongo"
	"github.com/codetrek/syntrix/internal/storage/internal/router"
	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"
	mongodriver "go.mongodb.org/mongo-driver/mongo"
)

// mongoProvider interface to allow mocking
type mongoProvider interface {
	Client() *mongodriver.Client
	DatabaseName() string
}

// Dependency injection for testing
var newMongoProvider = func(ctx context.Context, uri, dbName string) (Provider, error) {
	return mongo.NewProvider(ctx, uri, dbName)
}

type factory struct {
	providers map[string]Provider
	docStore  types.DocumentStore
	usrStore  types.UserStore
	revStore  types.TokenRevocationStore
	mu        sync.Mutex
}

func NewFactory(ctx context.Context, cfg *config.Config) (StorageFactory, error) {
	f := &factory{
		providers: make(map[string]Provider),
	}
	success := false
	defer func() {
		if !success {
			f.Close()
		}
	}()

	// 1. Initialize Providers
	for name, backendCfg := range cfg.Storage.Backends {
		if backendCfg.Type == "mongo" {
			p, err := newMongoProvider(ctx, backendCfg.Mongo.URI, backendCfg.Mongo.DatabaseName)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize backend %s: %w", name, err)
			}
			f.providers[name] = p
		} else {
			return nil, fmt.Errorf("unsupported backend type: %s", backendCfg.Type)
		}
	}

	// 2. Initialize Document Store
	defaultDocRouter, err := f.createDocumentRouter(cfg.Storage.Topology.Document)
	if err != nil {
		return nil, err
	}

	tenantDocRouters := make(map[string]types.DocumentRouter)
	for tID, tCfg := range cfg.Storage.Tenants {
		if tID == model.DefaultTenantID {
			continue
		}
		p, err := f.getMongoProvider(tCfg.Backend)
		if err != nil {
			return nil, err
		}
		store := mongo.NewDocumentStore(p.Client(), p.Client().Database(p.DatabaseName()), cfg.Storage.Topology.Document.DataCollection, cfg.Storage.Topology.Document.SysCollection, cfg.Storage.Topology.Document.SoftDeleteRetention)
		tenantDocRouters[tID] = router.NewSingleDocumentRouter(store)
	}
	f.docStore = router.NewRoutedDocumentStore(router.NewTenantDocumentRouter(defaultDocRouter, tenantDocRouters))

	// 3. Initialize User Store
	defaultUserRouter, err := f.createUserRouter(cfg.Storage.Topology.User)
	if err != nil {
		return nil, err
	}

	tenantUserRouters := make(map[string]types.UserRouter)
	for tID, tCfg := range cfg.Storage.Tenants {
		if tID == model.DefaultTenantID {
			continue
		}
		p, err := f.getMongoProvider(tCfg.Backend)
		if err != nil {
			return nil, err
		}
		store := mongo.NewUserStore(p.Client().Database(p.DatabaseName()), cfg.Storage.Topology.User.Collection)
		tenantUserRouters[tID] = router.NewSingleUserRouter(store)
	}
	f.usrStore = router.NewRoutedUserStore(router.NewTenantUserRouter(defaultUserRouter, tenantUserRouters))

	// 4. Initialize Revocation Store
	defaultRevRouter, err := f.createRevocationRouter(cfg.Storage.Topology.Revocation)
	if err != nil {
		return nil, err
	}

	tenantRevRouters := make(map[string]types.RevocationRouter)
	for tID, tCfg := range cfg.Storage.Tenants {
		if tID == model.DefaultTenantID {
			continue
		}
		p, err := f.getMongoProvider(tCfg.Backend)
		if err != nil {
			return nil, err
		}
		store := mongo.NewRevocationStore(p.Client().Database(p.DatabaseName()), cfg.Storage.Topology.Revocation.Collection)
		tenantRevRouters[tID] = router.NewSingleRevocationRouter(store)
	}
	f.revStore = router.NewRoutedRevocationStore(router.NewTenantRevocationRouter(defaultRevRouter, tenantRevRouters))
	success = true

	return f, nil
}

func (f *factory) createDocumentRouter(cfg config.DocumentTopology) (types.DocumentRouter, error) {
	primary, err := f.getMongoProvider(cfg.Primary)
	if err != nil {
		return nil, err
	}

	primaryStore := mongo.NewDocumentStore(primary.Client(), primary.Client().Database(primary.DatabaseName()), cfg.DataCollection, cfg.SysCollection, cfg.SoftDeleteRetention)

	switch cfg.Strategy {
	case "single":
		return router.NewSingleDocumentRouter(primaryStore), nil
	case "read_write_split":
		replica, err := f.getMongoProvider(cfg.Replica)
		if err != nil {
			return nil, err
		}
		replicaStore := mongo.NewDocumentStore(replica.Client(), replica.Client().Database(replica.DatabaseName()), cfg.DataCollection, cfg.SysCollection, cfg.SoftDeleteRetention)
		return router.NewSplitDocumentRouter(primaryStore, replicaStore), nil
	}

	return nil, fmt.Errorf("unsupported strategy: %s", cfg.Strategy)
}

func (f *factory) createUserRouter(cfg config.CollectionTopology) (types.UserRouter, error) {
	primary, err := f.getMongoProvider(cfg.Primary)
	if err != nil {
		return nil, err
	}

	primaryStore := mongo.NewUserStore(primary.Client().Database(primary.DatabaseName()), cfg.Collection)

	switch cfg.Strategy {
	case "single":
		return router.NewSingleUserRouter(primaryStore), nil
	case "read_write_split":
		replica, err := f.getMongoProvider(cfg.Replica)
		if err != nil {
			return nil, err
		}
		replicaStore := mongo.NewUserStore(replica.Client().Database(replica.DatabaseName()), cfg.Collection)
		return router.NewSplitUserRouter(primaryStore, replicaStore), nil
	}

	return nil, fmt.Errorf("unsupported strategy: %s", cfg.Strategy)
}

func (f *factory) createRevocationRouter(cfg config.CollectionTopology) (types.RevocationRouter, error) {
	primary, err := f.getMongoProvider(cfg.Primary)
	if err != nil {
		return nil, err
	}

	primaryStore := mongo.NewRevocationStore(primary.Client().Database(primary.DatabaseName()), cfg.Collection)

	switch cfg.Strategy {
	case "single":
		return router.NewSingleRevocationRouter(primaryStore), nil
	case "read_write_split":
		replica, err := f.getMongoProvider(cfg.Replica)
		if err != nil {
			return nil, err
		}
		replicaStore := mongo.NewRevocationStore(replica.Client().Database(replica.DatabaseName()), cfg.Collection)
		return router.NewSplitRevocationRouter(primaryStore, replicaStore), nil
	}

	return nil, fmt.Errorf("unsupported strategy: %s", cfg.Strategy)
}

func (f *factory) getMongoProvider(name string) (mongoProvider, error) {
	p, ok := f.providers[name]
	if !ok {
		return nil, fmt.Errorf("backend not found: %s", name)
	}
	mp, ok := p.(mongoProvider)
	if !ok {
		return nil, fmt.Errorf("backend %s is not a mongo provider", name)
	}
	return mp, nil
}

func (f *factory) GetMongoClient(name string) (*mongodriver.Client, string, error) {
	p, err := f.getMongoProvider(name)
	if err != nil {
		return nil, "", err
	}
	return p.Client(), p.DatabaseName(), nil
}

func (f *factory) Document() types.DocumentStore {
	return f.docStore
}

func (f *factory) User() types.UserStore {
	return f.usrStore
}

func (f *factory) Revocation() types.TokenRevocationStore {
	return f.revStore
}

func (f *factory) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var errs []error
	for _, p := range f.providers {
		if err := p.Close(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing providers: %v", errs)
	}
	return nil
}
