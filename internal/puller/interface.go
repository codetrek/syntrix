// Package puller provides committed change-event subscriptions with memory live
// delivery and durable replay. Local and gRPC consumers share cursor semantics.
package puller

import (
	"context"
	"errors"
	"log/slog"

	"github.com/syntrixbase/syntrix/internal/puller/client"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/core"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	pullergrpc "github.com/syntrixbase/syntrix/internal/puller/grpc"
	"github.com/syntrixbase/syntrix/internal/puller/health"
	"go.mongodb.org/mongo-driver/mongo"
)

// Service reports invalid starting positions at subscription and terminal
// failures through Next. Empty After starts at the captured committed head.
type Service interface {
	Subscribe(context.Context, SubscribeOptions) (Subscription, error)
}

type LocalService interface {
	Service
	AddBackend(string, *mongo.Client, string, config.PullerBackendConfig) error
	Start(context.Context) error
	Stop(context.Context) error
	BackendNames() []string
	Err() error
}

func NewService(cfg config.Config, logger *slog.Logger) LocalService {
	return core.New(cfg, logger)
}

func NewClient(address string, logger *slog.Logger) (Service, error) {
	if address == "" {
		return nil, errors.New("puller address cannot be empty")
	}
	return client.New(address, logger)
}

func NewGRPCServer(cfg config.GRPCConfig, svc Service, logger *slog.Logger) *GRPCServer {
	return pullergrpc.NewServer(cfg, svc, logger)
}

type Subscription = events.Subscription
type SubscribeOptions = events.SubscribeOptions
type Event = events.PullerEvent
type ChangeEvent = events.StoreChangeEvent
type UpdateDescription = events.UpdateDescription
type TruncatedArray = events.TruncatedArray
type ClusterTime = events.ClusterTime
type OperationType = events.StoreOperationType
type HealthChecker = health.Checker
type HealthReport = health.Report
type HealthStatus = health.Status
type GRPCServer = pullergrpc.Server

const (
	HealthOK               = health.StatusOK
	HealthDegraded         = health.StatusDegraded
	HealthUnhealthy        = health.StatusUnhealthy
	BootstrapFromNow       = health.BootstrapFromNow
	BootstrapFromBeginning = health.BootstrapFromBeginning
	OperationInsert        = events.StoreOperationInsert
	OperationUpdate        = events.StoreOperationUpdate
	OperationReplace       = events.StoreOperationReplace
	OperationDelete        = events.StoreOperationDelete
)

func NewHealthChecker(logger *slog.Logger) *HealthChecker { return health.NewChecker(logger) }
func StartHealthServer(ctx context.Context, addr string, checker *HealthChecker) error {
	return health.StartServer(ctx, addr, checker)
}
