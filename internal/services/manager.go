package services

import (
	"context"
	"net/http"
	"sync"

	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/auth"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"

	"github.com/nats-io/nats.go"
)

type Options struct {
	RunAPI              bool
	RunCSP              bool
	RunQuery            bool
	RunTriggerEvaluator bool
	RunTriggerWorker    bool

	ForceQueryClient bool
}

type triggerService interface {
	Watch(ctx context.Context, backend storage.DocumentStore) error
	LoadTriggers(triggers []*trigger.Trigger)
}

type triggerConsumer interface {
	Start(ctx context.Context) error
}

type Manager struct {
	cfg             *config.Config
	opts            Options
	servers         []*http.Server
	serverNames     []string
	docProvider     storage.DocumentProvider
	authProvider    storage.AuthProvider
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
