package services

import (
	"context"
	"net/http"
	"sync"

	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
)

// DeploymentMode represents the deployment mode of the service.
type DeploymentMode int

const (
	// ModeDistributed is the default mode where services communicate via HTTP/NATS.
	ModeDistributed DeploymentMode = iota
	// ModeStandalone runs all services in a single process with direct function calls.
	ModeStandalone
)

type Options struct {
	RunAPI              bool
	RunCSP              bool
	RunQuery            bool
	RunTriggerEvaluator bool
	RunTriggerWorker    bool
	ListenHost          string

	ForceQueryClient bool

	// Mode specifies the deployment mode (distributed or standalone).
	Mode DeploymentMode
}

type triggerService interface {
	Start(ctx context.Context) error
	LoadTriggers(triggers []*trigger.Trigger) error
}

type triggerConsumer interface {
	Start(ctx context.Context) error
}

type Manager struct {
	cfg             *config.Config
	opts            Options
	servers         []*http.Server
	serverNames     []string
	storageFactory  storage.StorageFactory
	docStore        storage.DocumentStore
	userStore       storage.UserStore
	revocationStore storage.TokenRevocationStore
	authService     identity.AuthN
	rtServer        *realtime.Server
	triggerConsumer triggerConsumer
	triggerService  triggerService
	natsProvider    trigger.NATSProvider
	wg              sync.WaitGroup
}

func NewManager(cfg *config.Config, opts Options) *Manager {
	if opts.ListenHost == "" {
		opts.ListenHost = "localhost"
	}

	return &Manager{
		cfg:  cfg,
		opts: opts,
	}
}

func (m *Manager) AuthService() identity.AuthN {
	return m.authService
}
