package engine

import (
	"context"
	"testing"

	"github.com/codetrek/syntrix/internal/trigger/internal/pubsub"
	"github.com/codetrek/syntrix/internal/trigger/internal/worker"
	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockTaskConsumer
type MockTaskConsumer struct {
	mock.Mock
}

func (m *MockTaskConsumer) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestNewFactory(t *testing.T) {
	f, err := NewFactory(nil, nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, f)
}

func TestFactoryOptions(t *testing.T) {
	f := &defaultTriggerFactory{}

	WithTenant("tenant1")(f)
	assert.Equal(t, "tenant1", f.tenant)

	WithStartFromNow(true)(f)
	assert.True(t, f.startFromNow)

	m := &types.NoopMetrics{}
	WithMetrics(m)(f)
	assert.Equal(t, m, f.metrics)

	// WithSecretProvider
	// We need a mock SecretProvider or nil
	WithSecretProvider(nil)(f)
	assert.Nil(t, f.secrets)
}

func TestFactory_Engine_Success(t *testing.T) {
	// If nats is nil, Engine() should succeed (with nil publisher)
	f, err := NewFactory(nil, nil, nil)
	assert.NoError(t, err)
	e := f.Engine()
	assert.NotNil(t, e)
}

func TestFactory_Engine_WithNATS(t *testing.T) {
	// Mock newTaskPublisher
	originalNewTaskPublisher := newTaskPublisher
	defer func() { newTaskPublisher = originalNewTaskPublisher }()

	mockPub := new(MockPublisher)
	newTaskPublisher = func(nc *nats.Conn, metrics types.Metrics) (pubsub.TaskPublisher, error) {
		return mockPub, nil
	}

	// Pass a dummy nats conn (can be nil if our mock doesn't check, but factory checks f.nats != nil)
	// We need f.nats != nil to trigger the branch.
	// But NewFactory takes *nats.Conn.
	// We can pass &nats.Conn{}
	f, err := NewFactory(nil, &nats.Conn{}, nil)
	assert.NoError(t, err)

	e := f.Engine()
	assert.NotNil(t, e)
}

func TestFactory_Consumer_Fail(t *testing.T) {
	// If nats is nil, Consumer() should fail because NewTaskConsumer fails
	f, err := NewFactory(nil, nil, nil)
	assert.NoError(t, err)
	c, err := f.Consumer(1)
	assert.Error(t, err)
	assert.Nil(t, c)
}

func TestFactory_Consumer_Success(t *testing.T) {
	// Mock newTaskConsumer
	originalNewTaskConsumer := newTaskConsumer
	defer func() { newTaskConsumer = originalNewTaskConsumer }()

	mockConsumer := new(MockTaskConsumer)
	newTaskConsumer = func(nc *nats.Conn, w worker.DeliveryWorker, numWorkers int, metrics types.Metrics) (pubsub.TaskConsumer, error) {
		return mockConsumer, nil
	}

	f, err := NewFactory(nil, &nats.Conn{}, nil)
	assert.NoError(t, err)

	c, err := f.Consumer(1)
	assert.NoError(t, err)
	assert.NotNil(t, c)
}

func TestFactory_Close(t *testing.T) {
	f, err := NewFactory(nil, nil, nil)
	assert.NoError(t, err)
	assert.NoError(t, f.Close())
}
