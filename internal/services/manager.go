package services

import (
	"context"
	"net/http"
	"sync"

	"syntrix/internal/api/realtime"
	"syntrix/internal/auth"
	"syntrix/internal/config"
	"syntrix/internal/storage"
	"syntrix/internal/trigger"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/nats-io/nats.go"
)

type Options struct {
	RunAPI              bool
	RunAuth             bool
	RunCSP              bool
	RunQuery            bool
	ForceQueryClient    bool
	RunTriggerEvaluator bool
	RunTriggerWorker    bool
}

type triggerService interface {
	Watch(ctx context.Context, backend storage.StorageBackend) error
	LoadTriggers(triggers []*trigger.Trigger)
}

type triggerConsumer interface {
	Start(ctx context.Context) error
}

type storageBackend interface {
	storage.StorageBackend
	DB() *mongo.Database
}

type authStorage interface {
	auth.StorageInterface
	EnsureIndexes(ctx context.Context) error
}

type Manager struct {
	cfg             *config.Config
	opts            Options
	servers         []*http.Server
	serverNames     []string
	storageBackend  storageBackend
	authService     auth.Service
	tokenService    *auth.TokenService
	rtServer        *realtime.Server
	triggerConsumer triggerConsumer
	triggerService  triggerService
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
