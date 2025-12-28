package engine

import (
	"fmt"

	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger/internal/evaluator"
	"github.com/codetrek/syntrix/internal/trigger/internal/pubsub"
	"github.com/codetrek/syntrix/internal/trigger/internal/watcher"
	"github.com/codetrek/syntrix/internal/trigger/internal/worker"
	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go"
)

var (
	newTaskPublisher = pubsub.NewTaskPublisher
	newTaskConsumer  = pubsub.NewTaskConsumer
)

// FactoryOption configures the factory.
type FactoryOption func(*defaultTriggerFactory)

// WithTenant sets the tenant for the factory.
func WithTenant(tenant string) FactoryOption {
	return func(f *defaultTriggerFactory) {
		f.tenant = tenant
	}
}

// WithStartFromNow sets whether to start watching from now if checkpoint is missing.
func WithStartFromNow(start bool) FactoryOption {
	return func(f *defaultTriggerFactory) {
		f.startFromNow = start
	}
}

// WithMetrics sets the metrics provider for the factory.
func WithMetrics(m types.Metrics) FactoryOption {
	return func(f *defaultTriggerFactory) {
		f.metrics = m
	}
}

// WithSecretProvider sets the secret provider for the factory.
func WithSecretProvider(s worker.SecretProvider) FactoryOption {
	return func(f *defaultTriggerFactory) {
		f.secrets = s
	}
}

// defaultTriggerFactory implements TriggerFactory.
type defaultTriggerFactory struct {
	store        storage.DocumentStore
	nats         *nats.Conn
	auth         identity.AuthN
	tenant       string
	startFromNow bool
	metrics      types.Metrics
	secrets      worker.SecretProvider
}

// NewFactory creates a new TriggerFactory.
func NewFactory(store storage.DocumentStore, nats *nats.Conn, auth identity.AuthN, opts ...FactoryOption) (TriggerFactory, error) {
	f := &defaultTriggerFactory{
		store:   store,
		nats:    nats,
		auth:    auth,
		tenant:  "default",
		metrics: &types.NoopMetrics{},
	}
	for _, opt := range opts {
		opt(f)
	}
	return f, nil
}

// Engine returns a new TriggerEngine.
func (f *defaultTriggerFactory) Engine() TriggerEngine {
eval, err := evaluator.NewEvaluator()
if err != nil {
panic(err)
}

w := watcher.NewWatcher(f.store, f.tenant, watcher.WatcherOptions{
StartFromNow: f.startFromNow,
})

	var pub pubsub.TaskPublisher
	if f.nats != nil {
		p, err := newTaskPublisher(f.nats, f.metrics)
		if err != nil {
			panic(err)
		}
		pub = p
	}

	return &defaultTriggerEngine{
		evaluator: eval,
		watcher:   w,
		publisher: pub,
	}
}

// Consumer returns a new TaskConsumer.
func (f *defaultTriggerFactory) Consumer(numWorkers int) (TaskConsumer, error) {
	if f.nats == nil {
		return nil, fmt.Errorf("nats connection is required for consumer")
	}

	w := worker.NewDeliveryWorker(f.auth, f.secrets, worker.HTTPClientOptions{}, f.metrics)

	return newTaskConsumer(f.nats, w, numWorkers, f.metrics)
}

// Close releases resources.
func (f *defaultTriggerFactory) Close() error {
	return nil
}
