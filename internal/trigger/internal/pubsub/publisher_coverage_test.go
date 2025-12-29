package pubsub

import (
	"context"
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockJetStreamCoverage struct {
	mock.Mock
	jetstream.JetStream
}

func (m *MockJetStreamCoverage) CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	args := m.Called(ctx, cfg)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(jetstream.Stream), args.Error(1)
}

func TestNewTaskPublisher_Coverage(t *testing.T) {
	// Save original jetStreamNew and restore after test
	originalJetStreamNew := jetStreamNew
	defer func() { jetStreamNew = originalJetStreamNew }()

	// Case 1: Nil connection
	pub, err := NewTaskPublisher(nil, nil)
	assert.Error(t, err)
	assert.Nil(t, pub)
	assert.Contains(t, err.Error(), "nats connection cannot be nil")

	// Case 2: jetStreamNew error
	jetStreamNew = func(nc *nats.Conn) (jetstream.JetStream, error) {
		return nil, errors.New("mock js error")
	}
	pub, err = NewTaskPublisher(&nats.Conn{}, nil)
	assert.Error(t, err)
	assert.Nil(t, pub)
	assert.Contains(t, err.Error(), "mock js error")

	// Case 3: EnsureStream error
	mockJS := new(MockJetStreamCoverage)
	mockJS.On("CreateOrUpdateStream", mock.Anything, mock.Anything).Return(nil, errors.New("stream error"))

	jetStreamNew = func(nc *nats.Conn) (jetstream.JetStream, error) {
		return mockJS, nil
	}

	pub, err = NewTaskPublisher(&nats.Conn{}, nil)
	assert.Error(t, err)
	assert.Nil(t, pub)
	assert.Contains(t, err.Error(), "failed to ensure stream")
	assert.Contains(t, err.Error(), "stream error")

	// Case 4: Success
	mockJS2 := new(MockJetStreamCoverage)
	mockJS2.On("CreateOrUpdateStream", mock.Anything, mock.Anything).Return(&MockStream{}, nil)

	jetStreamNew = func(nc *nats.Conn) (jetstream.JetStream, error) {
		return mockJS2, nil
	}

	pub, err = NewTaskPublisher(&nats.Conn{}, nil)
	assert.NoError(t, err)
	assert.NotNil(t, pub)
}
