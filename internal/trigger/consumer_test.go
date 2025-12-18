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

func (m *MockConsumer) Consume(handler jetstream.MessageHandler, opts ...jetstream.PullConsumeOpt) (jetstream.ConsumeContext, error) {
	args := m.Called(handler)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(jetstream.ConsumeContext), args.Error(1)
}

type MockConsumeContext struct {
	mock.Mock
	jetstream.ConsumeContext
}

func (m *MockConsumeContext) Stop() {
	m.Called()
}

func (m *MockConsumeContext) Drain() {
	m.Called()
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

func (m *MockMsg) Metadata() (*jetstream.MsgMetadata, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*jetstream.MsgMetadata), args.Error(1)
}

func (m *MockMsg) Term() error {
	args := m.Called()
	return args.Error(0)
}

// --- Tests ---

func TestConsumer_Start_Success(t *testing.T) {
	// Setup Mocks
	js := new(MockJetStream)
	worker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)
	msg := new(MockMsg)

	c := &Consumer{
		js:         js,
		worker:     worker,
		stream:     "TRIGGERS",
		numWorkers: 1,
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

	// 3. Consume Messages
	consumeCtx := new(MockConsumeContext)
	consumer.On("Consume", mock.Anything).Return(consumeCtx, nil).Run(func(args mock.Arguments) {
		handler := args.Get(0).(jetstream.MessageHandler)

		// Simulate receiving a message
		task := DeliveryTask{TriggerID: "t1"}
		taskBytes, _ := json.Marshal(task)

		msg.On("Data").Return(taskBytes)
		msg.On("Ack").Return(nil)

		// Call handler in a goroutine to simulate async behavior
		go func() {
			handler(msg)
			// After handling, we can cancel the context to stop the test
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
	})

	consumeCtx.On("Stop").Return()

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
	consumeCtx.AssertExpectations(t)
	msg.AssertExpectations(t)
	worker.AssertExpectations(t)
}

func TestConsumer_Start_ProcessError(t *testing.T) {
	// Setup Mocks
	js := new(MockJetStream)
	worker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)
	msg := new(MockMsg)

	c := &Consumer{
		js:         js,
		worker:     worker,
		stream:     "TRIGGERS",
		numWorkers: 1,
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Expectations
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(stream, nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)

	consumeCtx := new(MockConsumeContext)
	consumer.On("Consume", mock.Anything).Return(consumeCtx, nil).Run(func(args mock.Arguments) {
		handler := args.Get(0).(jetstream.MessageHandler)

		task := DeliveryTask{TriggerID: "t1"}
		taskBytes, _ := json.Marshal(task)

		msg.On("Data").Return(taskBytes)
		msg.On("NakWithDelay", mock.Anything).Return(nil)
		msg.On("Metadata").Return(&jetstream.MsgMetadata{NumDelivered: 1}, nil)

		go func() {
			handler(msg)
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
	})
	consumeCtx.On("Stop").Return()

	worker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("failed"))

	err := c.Start(ctx)
	assert.NoError(t, err)
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

	// 3. Consume Error
	consumer := new(MockConsumer)
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(new(MockStream), nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)
	consumer.On("Consume", mock.Anything).Return(nil, errors.New("consume error")).Once()
	err = c.Start(ctx)
	assert.ErrorContains(t, err, "failed to start consumer")
}

func TestConsumer_Start_InvalidPayload(t *testing.T) {
	js := new(MockJetStream)
	worker := new(MockWorker)
	consumer := new(MockConsumer)
	msg := new(MockMsg)

	c := &Consumer{js: js, worker: worker, stream: "TRIGGERS", numWorkers: 1}
	ctx, cancel := context.WithCancel(context.Background())

	// Setup happy path for init
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(new(MockStream), nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)

	consumeCtx := new(MockConsumeContext)
	consumer.On("Consume", mock.Anything).Return(consumeCtx, nil).Run(func(args mock.Arguments) {
		handler := args.Get(0).(jetstream.MessageHandler)

		// Invalid JSON
		msg.On("Data").Return([]byte("invalid-json"))
		msg.On("Term").Return(nil)

		go func() {
			handler(msg)
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
	})
	consumeCtx.On("Stop").Return()

	err := c.Start(ctx)
	assert.NoError(t, err)

	msg.AssertExpectations(t)
	// Worker should NOT be called
	worker.AssertNotCalled(t, "ProcessTask")
}
