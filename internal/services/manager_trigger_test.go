package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/identity"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/codetrek/syntrix/internal/trigger/engine"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockFactory implements engine.TriggerFactory for testing
type MockFactory struct {
	mock.Mock
}

func (m *MockFactory) Engine() (engine.TriggerEngine, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(engine.TriggerEngine), args.Error(1)
}

func (m *MockFactory) Consumer(numWorkers int) (engine.TaskConsumer, error) {
	args := m.Called(numWorkers)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(engine.TaskConsumer), args.Error(1)
}

func (m *MockFactory) Close() error {
	args := m.Called()
	return args.Error(0)
}

func TestManager_InitTriggerServices_Success_WithHooks(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Trigger.RulesFile = ""
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true, RunTriggerWorker: true})

	origConnector := trigger.GetNatsConnectFunc()
	origFactory := triggerFactoryFactory
	defer func() {
		trigger.SetNatsConnectFunc(origConnector)
		triggerFactoryFactory = origFactory
	}()

	fakeConn := &nats.Conn{}
	trigger.SetNatsConnectFunc(func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil })

	mockFactory := new(MockFactory)
	triggerFactoryFactory = func(storage.DocumentStore, *nats.Conn, identity.AuthN, ...engine.FactoryOption) (engine.TriggerFactory, error) {
		return mockFactory, nil
	}

	cons := &fakeConsumer{}
	mockFactory.On("Consumer", cfg.Trigger.WorkerCount).Return(cons, nil)

	eval := &fakeEvaluator{}
	mockFactory.On("Engine").Return(eval, nil)

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.NotNil(t, mgr.triggerService)
	assert.Same(t, fakeConn, mgr.natsConn)
	mockFactory.AssertExpectations(t)
}

func TestManager_InitTriggerServices_WorkerOnly(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	origConnector := trigger.GetNatsConnectFunc()
	origFactory := triggerFactoryFactory
	defer func() {
		trigger.SetNatsConnectFunc(origConnector)
		triggerFactoryFactory = origFactory
	}()

	fakeConn := &nats.Conn{}
	trigger.SetNatsConnectFunc(func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil })

	mockFactory := new(MockFactory)
	triggerFactoryFactory = func(storage.DocumentStore, *nats.Conn, identity.AuthN, ...engine.FactoryOption) (engine.TriggerFactory, error) {
		return mockFactory, nil
	}

	cons := &fakeConsumer{}
	mockFactory.On("Consumer", cfg.Trigger.WorkerCount).Return(cons, nil)

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.Same(t, fakeConn, mgr.natsConn)
	mockFactory.AssertExpectations(t)
}

func TestManager_InitTriggerServices_EvaluatorOnly_WithRules(t *testing.T) {
	cfg := config.LoadConfig()
	tmpDir := t.TempDir()
	jsonRules := `[{"id":"t1","collection":"*","events":["create"],"condition":""}]`
	rulesFile := filepath.Join(tmpDir, "rules.json")
	assert.NoError(t, os.WriteFile(rulesFile, []byte(jsonRules), 0644))
	cfg.Trigger.RulesFile = rulesFile

	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	origConnector := trigger.GetNatsConnectFunc()
	origFactory := triggerFactoryFactory
	defer func() {
		trigger.SetNatsConnectFunc(origConnector)
		triggerFactoryFactory = origFactory
	}()

	fakeConn := &nats.Conn{}
	trigger.SetNatsConnectFunc(func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil })

	mockFactory := new(MockFactory)
	triggerFactoryFactory = func(storage.DocumentStore, *nats.Conn, identity.AuthN, ...engine.FactoryOption) (engine.TriggerFactory, error) {
		return mockFactory, nil
	}

	eval := &fakeEvaluator{}
	mockFactory.On("Engine").Return(eval, nil)

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.NotNil(t, mgr.triggerService)
	mockFactory.AssertExpectations(t)
}

func TestManager_InitTriggerServices_ConsumerError(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	origConnector := trigger.GetNatsConnectFunc()
	origFactory := triggerFactoryFactory
	defer func() {
		trigger.SetNatsConnectFunc(origConnector)
		triggerFactoryFactory = origFactory
	}()

	fakeConn := &nats.Conn{}
	trigger.SetNatsConnectFunc(func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil })

	mockFactory := new(MockFactory)
	triggerFactoryFactory = func(storage.DocumentStore, *nats.Conn, identity.AuthN, ...engine.FactoryOption) (engine.TriggerFactory, error) {
		return mockFactory, nil
	}

	mockFactory.On("Consumer", cfg.Trigger.WorkerCount).Return(nil, fmt.Errorf("cons error"))

	err := mgr.initTriggerServices()
	assert.ErrorContains(t, err, "cons error")
	mockFactory.AssertExpectations(t)
}

func TestManager_InitTriggerServices_NatsError(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	origConnector := trigger.GetNatsConnectFunc()
	defer func() { trigger.SetNatsConnectFunc(origConnector) }()

	trigger.SetNatsConnectFunc(func(string, ...nats.Option) (*nats.Conn, error) {
		return nil, fmt.Errorf("nats error")
	})

	err := mgr.initTriggerServices()
	assert.ErrorContains(t, err, "nats error")
}

func TestManager_InitTriggerServices_StandaloneEmbeddedNATS(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Deployment.Mode = "standalone"
	cfg.Deployment.Standalone.EmbeddedNATS = true
	cfg.Deployment.Standalone.NATSDataDir = "/tmp/nats"
	mgr := NewManager(cfg, Options{Mode: ModeStandalone, RunTriggerEvaluator: true})

	// EmbeddedNATSProvider should return error since it's not implemented
	err := mgr.initTriggerServices()
	assert.Error(t, err)
	assert.ErrorIs(t, err, trigger.ErrEmbeddedNATSNotImplemented)
}

func TestManager_InitTriggerServices_StandaloneRemoteNATS(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Deployment.Mode = "standalone"
	cfg.Deployment.Standalone.EmbeddedNATS = false // Use remote NATS
	mgr := NewManager(cfg, Options{Mode: ModeStandalone, RunTriggerEvaluator: true})

	origConnector := trigger.GetNatsConnectFunc()
	origFactory := triggerFactoryFactory
	defer func() {
		trigger.SetNatsConnectFunc(origConnector)
		triggerFactoryFactory = origFactory
	}()

	fakeConn := &nats.Conn{}
	trigger.SetNatsConnectFunc(func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil })

	mockFactory := new(MockFactory)
	triggerFactoryFactory = func(storage.DocumentStore, *nats.Conn, identity.AuthN, ...engine.FactoryOption) (engine.TriggerFactory, error) {
		return mockFactory, nil
	}

	eval := &fakeEvaluator{}
	mockFactory.On("Engine").Return(eval, nil)

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.NotNil(t, mgr.natsProvider)
	mockFactory.AssertExpectations(t)
}

// Fakes

type fakeEvaluator struct{}

func (f *fakeEvaluator) LoadTriggers(triggers []*trigger.Trigger) error {
	return nil
}

func (f *fakeEvaluator) Start(ctx context.Context) error {
	return nil
}

func (f *fakeEvaluator) Close() error {
	return nil
}

type fakeConsumer struct{}

func (f *fakeConsumer) Start(ctx context.Context) error {
	return nil
}

type fakePublisher struct{ created bool }

func (f *fakePublisher) Publish(context.Context, *trigger.DeliveryTask) error {
	f.created = true
	return nil
}

func (f *fakePublisher) Close() {}
