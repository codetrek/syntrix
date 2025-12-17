package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type MockWorker struct {
	mock.Mock
}

func (m *MockWorker) ProcessTask(ctx context.Context, task *DeliveryTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
}

type MockJetStream struct {
	mock.Mock
	jetstream.JetStream
}

func (m *MockJetStream) CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	args := m.Called(ctx, cfg)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(jetstream.Stream), args.Error(1)
}

func (m *MockJetStream) CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	args := m.Called(ctx, stream, cfg)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(jetstream.Consumer), args.Error(1)
}

type MockStream struct {
	mock.Mock
	jetstream.Stream
}

type MockConsumer struct {
	mock.Mock
	jetstream.Consumer
}

func (m *MockConsumer) Messages(opts ...jetstream.PullMessagesOpt) (jetstream.MessagesContext, error) {
	args := m.Called(opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(jetstream.MessagesContext), args.Error(1)
}

type MockMessagesContext struct {
	mock.Mock
	jetstream.MessagesContext
}

func (m *MockMessagesContext) Next(opts ...jetstream.NextOpt) (jetstream.Msg, error) {
	args := m.Called() // We ignore opts for now in mock expectations
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(jetstream.Msg), args.Error(1)
}

func (m *MockMessagesContext) Stop() {
	m.Called()
}

func (m *MockMessagesContext) Drain() {
	m.Called()
}

type MockMsg struct {
	mock.Mock
	jetstream.Msg
}

func (m *MockMsg) Data() []byte {
	args := m.Called()
	return args.Get(0).([]byte)
}

func (m *MockMsg) Ack() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMsg) Nak() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockMsg) NakWithDelay(delay time.Duration) error {
	args := m.Called(delay)
	return args.Error(0)
}

// --- Tests ---

func TestConsumer_Start_Success(t *testing.T) {
	// Setup Mocks
	js := new(MockJetStream)
	worker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)
	iter := new(MockMessagesContext)
	msg := new(MockMsg)

	c := &Consumer{
		js:     js,
		worker: worker,
		stream: "TRIGGERS",
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Expectations
	// 1. Create Stream
	js.On("CreateOrUpdateStream", ctx, mock.MatchedBy(func(cfg jetstream.StreamConfig) bool {
		return cfg.Name == "TRIGGERS"
	})).Return(stream, nil)

	// 2. Create Consumer
	js.On("CreateOrUpdateConsumer", ctx, "TRIGGERS", mock.MatchedBy(func(cfg jetstream.ConsumerConfig) bool {
		return cfg.Durable == "TriggerDeliveryWorker"
	})).Return(consumer, nil)

	// 3. Get Messages Iterator
	consumer.On("Messages", mock.Anything).Return(iter, nil)

	// 4. Fetch Message (Return one message then block/stop)
	task := DeliveryTask{TriggerID: "t1"}
	taskBytes, _ := json.Marshal(task)

	msg.On("Data").Return(taskBytes)
	msg.On("Ack").Return(nil)

	// First call returns message
	iter.On("Next").Return(msg, nil).Once()
	// Second call blocks until context done (simulated by returning error or blocking)
	// Here we simulate context cancellation by returning error after a short delay or just error
	iter.On("Next").Return(nil, errors.New("context canceled")).Run(func(args mock.Arguments) {
		cancel() // Cancel context to stop loop
	})

	iter.On("Stop").Return()

	// 5. Process Task
	worker.On("ProcessTask", mock.Anything, mock.MatchedBy(func(t *DeliveryTask) bool {
		return t.TriggerID == "t1"
	})).Return(nil)

	// Run
	err := c.Start(ctx)
	assert.NoError(t, err)

	// Verify
	js.AssertExpectations(t)
	consumer.AssertExpectations(t)
	iter.AssertExpectations(t)
	msg.AssertExpectations(t)
	worker.AssertExpectations(t)
}

func TestConsumer_Start_ProcessError(t *testing.T) {
	// Setup Mocks
	js := new(MockJetStream)
	worker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)
	iter := new(MockMessagesContext)
	msg := new(MockMsg)

	c := &Consumer{
		js:     js,
		worker: worker,
		stream: "TRIGGERS",
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Expectations
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(stream, nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)
	consumer.On("Messages", mock.Anything).Return(iter, nil)

	task := DeliveryTask{TriggerID: "t1"}
	taskBytes, _ := json.Marshal(task)

	msg.On("Data").Return(taskBytes)
	msg.On("Nak").Return(nil) // Expect Nak on error

	iter.On("Next").Return(msg, nil).Once()
	iter.On("Next").Return(nil, errors.New("stop")).Run(func(args mock.Arguments) {
		cancel()
	})
	iter.On("Stop").Return()

	// Worker returns error
	worker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("failed"))

	// Run
	err := c.Start(ctx)
	assert.NoError(t, err)

	msg.AssertExpectations(t)
	worker.AssertExpectations(t)
}

func TestConsumer_Start_InitErrors(t *testing.T) {
	js := new(MockJetStream)
	worker := new(MockWorker)
	c := &Consumer{js: js, worker: worker, stream: "TRIGGERS"}
	ctx := context.Background()

	// 1. Stream Creation Error
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(nil, errors.New("stream error")).Once()
	err := c.Start(ctx)
	assert.ErrorContains(t, err, "failed to ensure stream")

	// 2. Consumer Creation Error
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(new(MockStream), nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("consumer error")).Once()
	err = c.Start(ctx)
	assert.ErrorContains(t, err, "failed to create consumer")

	// 3. Iterator Creation Error
	consumer := new(MockConsumer)
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(new(MockStream), nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)
	consumer.On("Messages", mock.Anything).Return(nil, errors.New("iter error")).Once()
	err = c.Start(ctx)
	assert.ErrorContains(t, err, "failed to create message iterator")
}

func TestConsumer_Start_InvalidPayload(t *testing.T) {
	js := new(MockJetStream)
	worker := new(MockWorker)
	consumer := new(MockConsumer)
	iter := new(MockMessagesContext)
	msg := new(MockMsg)

	c := &Consumer{js: js, worker: worker, stream: "TRIGGERS"}
	ctx, cancel := context.WithCancel(context.Background())

	// Setup happy path for init
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(new(MockStream), nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)
	consumer.On("Messages", mock.Anything).Return(iter, nil)

	// Invalid JSON
	msg.On("Data").Return([]byte("invalid-json"))
	// Should log error and continue (which means Nak in current implementation? No, processMsg returns error, so Nak)
	// Wait, processMsg returns error on unmarshal failure.
	// Loop calls processMsg -> error -> msg.Nak()
	msg.On("Nak").Return(nil)

	iter.On("Next").Return(msg, nil).Once()
	iter.On("Next").Return(nil, errors.New("stop")).Run(func(args mock.Arguments) { cancel() })
	iter.On("Stop").Return()

	err := c.Start(ctx)
	assert.NoError(t, err)

	msg.AssertExpectations(t)
	// Worker should NOT be called
	worker.AssertNotCalled(t, "ProcessTask")
}
