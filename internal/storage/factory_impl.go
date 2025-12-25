package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage/internal/mongo"
	"github.com/codetrek/syntrix/internal/storage/internal/router"
	"github.com/codetrek/syntrix/internal/storage/types"
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

	// 1. Initialize Providers
	for name, backendCfg := range cfg.Storage.Backends {
		if backendCfg.Type == "mongo" {
			p, err := newMongoProvider(ctx, backendCfg.Mongo.URI, backendCfg.Mongo.DatabaseName)
			if err != nil {
				f.Close() // Cleanup any already opened
				return nil, fmt.Errorf("failed to initialize backend %s: %w", name, err)
			}
			f.providers[name] = p
		} else {
			return nil, fmt.Errorf("unsupported backend type: %s", backendCfg.Type)
		}
	}

	// 2. Initialize Document Store
	docRouter, err := f.createDocumentRouter(cfg.Storage.Topology.Document)
	if err != nil {
		f.Close()
		return nil, err
	}
	f.docStore = router.NewRoutedDocumentStore(docRouter)

	// 3. Initialize User Store
	usrRouter, err := f.createUserRouter(cfg.Storage.Topology.User)
	if err != nil {
		f.Close()
		return nil, err
	}
	f.usrStore = router.NewRoutedUserStore(usrRouter)

	// 4. Initialize Revocation Store
	revRouter, err := f.createRevocationRouter(cfg.Storage.Topology.Revocation)
	if err != nil {
		f.Close()
		return nil, err
	}
	f.revStore = router.NewRoutedRevocationStore(revRouter)

	return f, nil
}

func (f *factory) createDocumentRouter(cfg config.DocumentTopology) (types.DocumentRouter, error) {
	primary, err := f.getMongoProvider(cfg.Primary)
	if err != nil {
		return nil, err
	}

	primaryStore := mongo.NewDocumentStore(primary.Client(), primary.Client().Database(primary.DatabaseName()), cfg.DataCollection, cfg.SysCollection, cfg.SoftDeleteRetention)

	if cfg.Strategy == "single" {
		return router.NewSingleDocumentRouter(primaryStore), nil
	} else if cfg.Strategy == "read_write_split" {
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

	if cfg.Strategy == "single" {
		return router.NewSingleUserRouter(primaryStore), nil
	} else if cfg.Strategy == "read_write_split" {
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

	if cfg.Strategy == "single" {
		return router.NewSingleRevocationRouter(primaryStore), nil
	} else if cfg.Strategy == "read_write_split" {
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
