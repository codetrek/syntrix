package pubsub

import (
	"testing"

	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewTaskConsumer_NilConn(t *testing.T) {
	_, err := NewTaskConsumer(nil, nil, 1, &types.NoopMetrics{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nats connection cannot be nil")
}

func TestNewTaskConsumer_Success(t *testing.T) {
	mockJS := new(MockJetStream)
	cleanup := SetJetStreamNew(func(nc *nats.Conn) (jetstream.JetStream, error) {
		return mockJS, nil
	})
	defer cleanup()

	// We need a non-nil nats.Conn, but since we mock jetStreamNew, it won't be used.
	// However, nats.Conn is a struct, we can pass &nats.Conn{}.
	// Note: passing an empty struct might cause issues if code accesses fields, but NewTaskConsumer only checks for nil.
	// Wait, NewTaskConsumerFromJS might use it? No, it uses JS interface.

	c, err := NewTaskConsumer(&nats.Conn{}, nil, 1, &types.NoopMetrics{})
	assert.NoError(t, err)
	assert.NotNil(t, c)
}

func TestNewTaskPublisher_NilConn(t *testing.T) {
	_, err := NewTaskPublisher(nil, &types.NoopMetrics{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nats connection cannot be nil")
}

func TestNewTaskPublisher_Success(t *testing.T) {
	mockJS := new(MockJetStream)
	cleanup := SetJetStreamNew(func(nc *nats.Conn) (jetstream.JetStream, error) {
		return mockJS, nil
	})
	defer cleanup()

	// EnsureStream will be called
	mockJS.On("CreateOrUpdateStream", mock.Anything, mock.Anything).Return(nil, nil)

	p, err := NewTaskPublisher(&nats.Conn{}, &types.NoopMetrics{})
	assert.NoError(t, err)
	assert.NotNil(t, p)
}

func TestEnsureStream(t *testing.T) {
	mockJS := new(MockJetStream)

	// Expect CreateOrUpdateStream to be called
	mockJS.On("CreateOrUpdateStream", mock.Anything, mock.MatchedBy(func(cfg jetstream.StreamConfig) bool {
		return cfg.Name == "TRIGGERS" && len(cfg.Subjects) > 0 && cfg.Subjects[0] == "triggers.>"
	})).Return(nil, nil)

	err := EnsureStream(mockJS)
	assert.NoError(t, err)
	mockJS.AssertExpectations(t)
}
