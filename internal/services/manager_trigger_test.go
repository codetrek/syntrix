package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
)

func TestManager_InitTriggerServices_Success_WithHooks(t *testing.T) {
	cfg := config.LoadConfig()
	cfg.Trigger.RulesFile = ""
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true, RunTriggerWorker: true})

	origConnector := natsConnector
	origPub := triggerPublisherFactory
	origCons := triggerConsumerFactory
	origEval := triggerEvaluatorFactory
	defer func() {
		natsConnector = origConnector
		triggerPublisherFactory = origPub
		triggerConsumerFactory = origCons
		triggerEvaluatorFactory = origEval
	}()

	fakeConn := &nats.Conn{}
	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil }

	pub := &fakePublisher{}
	triggerPublisherFactory = func(*nats.Conn) (trigger.EventPublisher, error) {
		pub.created = true
		return pub, nil
	}

	triggerConsumerFactory = func(_ *nats.Conn, _ trigger.Worker, _ int) (*trigger.Consumer, error) {
		return (*trigger.Consumer)(nil), nil
	}

	triggerEvaluatorFactory = func() (trigger.Evaluator, error) { return &fakeEvaluator{}, nil }

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.NotNil(t, mgr.triggerService)
	assert.Same(t, fakeConn, mgr.natsConn)
	assert.True(t, pub.created)
}

func TestManager_InitTriggerServices_WorkerOnly(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	origConnector := natsConnector
	origCons := triggerConsumerFactory
	defer func() {
		natsConnector = origConnector
		triggerConsumerFactory = origCons
	}()

	fakeConn := &nats.Conn{}
	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil }

	consCreated := false
	triggerConsumerFactory = func(_ *nats.Conn, _ trigger.Worker, _ int) (*trigger.Consumer, error) {
		consCreated = true
		return (*trigger.Consumer)(nil), nil
	}

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.True(t, consCreated)
	assert.Same(t, fakeConn, mgr.natsConn)
}

func TestManager_InitTriggerServices_EvaluatorOnly_WithRules(t *testing.T) {
	cfg := config.LoadConfig()
	tmpDir := t.TempDir()
	jsonRules := `[{"id":"t1","collection":"*","events":["create"],"condition":""}]`
	rulesFile := filepath.Join(tmpDir, "rules.json")
	assert.NoError(t, os.WriteFile(rulesFile, []byte(jsonRules), 0644))
	cfg.Trigger.RulesFile = rulesFile

	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	origConnector := natsConnector
	origPub := triggerPublisherFactory
	origEval := triggerEvaluatorFactory
	defer func() {
		natsConnector = origConnector
		triggerPublisherFactory = origPub
		triggerEvaluatorFactory = origEval
	}()

	fakeConn := &nats.Conn{}
	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return fakeConn, nil }

	pub := &fakePublisher{}
	triggerPublisherFactory = func(*nats.Conn) (trigger.EventPublisher, error) {
		pub.created = true
		return pub, nil
	}

	triggerEvaluatorFactory = func() (trigger.Evaluator, error) { return &fakeEvaluator{}, nil }

	err := mgr.initTriggerServices()
	assert.NoError(t, err)
	assert.NotNil(t, mgr.triggerService)
	assert.True(t, pub.created)
}

func TestManager_InitTriggerServices_PublisherError(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	origConnector := natsConnector
	origPub := triggerPublisherFactory
	origEval := triggerEvaluatorFactory
	defer func() {
		natsConnector = origConnector
		triggerPublisherFactory = origPub
		triggerEvaluatorFactory = origEval
	}()

	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return &nats.Conn{}, nil }
	triggerEvaluatorFactory = func() (trigger.Evaluator, error) { return &fakeEvaluator{}, nil }
	triggerPublisherFactory = func(*nats.Conn) (trigger.EventPublisher, error) { return nil, fmt.Errorf("pub err") }

	err := mgr.initTriggerServices()
	assert.Error(t, err)
}

func TestManager_InitTriggerServices_EvaluatorError(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	origConnector := natsConnector
	origEval := triggerEvaluatorFactory
	defer func() {
		natsConnector = origConnector
		triggerEvaluatorFactory = origEval
	}()

	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return &nats.Conn{}, nil }
	triggerEvaluatorFactory = func() (trigger.Evaluator, error) { return nil, fmt.Errorf("eval err") }

	err := mgr.initTriggerServices()
	assert.Error(t, err)
}

func TestManager_InitTriggerServices_NatsConnectError(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true, RunTriggerWorker: true})

	origConnector := natsConnector
	defer func() { natsConnector = origConnector }()

	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return nil, fmt.Errorf("connect err") }

	err := mgr.initTriggerServices()
	assert.Error(t, err)
	assert.Nil(t, mgr.natsConn)
}

func TestManager_InitTriggerServices_WorkerInitError(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	origConnector := natsConnector
	origCons := triggerConsumerFactory
	defer func() {
		natsConnector = origConnector
		triggerConsumerFactory = origCons
	}()

	natsConnector = func(string, ...nats.Option) (*nats.Conn, error) { return &nats.Conn{}, nil }

	triggerConsumerFactory = func(_ *nats.Conn, _ trigger.Worker, _ int) (*trigger.Consumer, error) {
		return nil, fmt.Errorf("cons err")
	}

	err := mgr.initTriggerServices()
	assert.Error(t, err)
}

type fakeEvaluator struct{}

func (f *fakeEvaluator) Evaluate(context.Context, *trigger.Trigger, *storage.Event) (bool, error) {
	return true, nil
}

func (f *fakeEvaluator) Validate(*trigger.Trigger) error { return nil }

type fakePublisher struct{ created bool }

func (f *fakePublisher) Publish(context.Context, *trigger.DeliveryTask) error {
	f.created = true
	return nil
}
