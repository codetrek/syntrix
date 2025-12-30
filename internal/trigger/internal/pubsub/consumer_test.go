package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/trigger"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type MockWorker struct {
	mock.Mock
}

func (m *MockWorker) ProcessTask(ctx context.Context, task *trigger.DeliveryTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
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
	t.Parallel()
	// Setup Mocks
	js := new(MockJetStream)
	mockWorker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)
	msg := new(MockMsg)

	c, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_Start_Success", 1, nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Expectations
	// 1. Create Stream
	js.On("CreateOrUpdateStream", ctx, mock.MatchedBy(func(cfg jetstream.StreamConfig) bool {
		return cfg.Name == "TestConsumer_Start_Success"
	})).Return(stream, nil)

	// 2. Create Consumer
	js.On("CreateOrUpdateConsumer", ctx, "TestConsumer_Start_Success", mock.MatchedBy(func(cfg jetstream.ConsumerConfig) bool {
		return cfg.Durable == "TriggerDeliveryWorker"
	})).Return(consumer, nil)

	// 3. Consume Messages
	consumeCtx := new(MockConsumeContext)
	consumer.On("Consume", mock.Anything).Return(consumeCtx, nil).Run(func(args mock.Arguments) {
		handler := args.Get(0).(jetstream.MessageHandler)

		// Simulate receiving a message
		task := trigger.DeliveryTask{TriggerID: "t1"}
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
	mockWorker.On("ProcessTask", mock.Anything, mock.MatchedBy(func(t *trigger.DeliveryTask) bool {
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
	mockWorker.AssertExpectations(t)
}

func TestConsumer_Start_ProcessError(t *testing.T) {
	t.Parallel()
	// Setup Mocks
	js := new(MockJetStream)
	mockWorker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)
	msg := new(MockMsg)

	c, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_Start_ProcessError", 1, nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Expectations
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(stream, nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)

	consumeCtx := new(MockConsumeContext)
	consumer.On("Consume", mock.Anything).Return(consumeCtx, nil).Run(func(args mock.Arguments) {
		handler := args.Get(0).(jetstream.MessageHandler)

		task := trigger.DeliveryTask{TriggerID: "t1"}
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

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("failed"))

	err := c.Start(ctx)
	assert.NoError(t, err)
}

func TestConsumer_Start_InitErrors(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)
	c, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_Start_InitErrors", 1, nil)
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
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)
	consumer := new(MockConsumer)
	msg := new(MockMsg)

	c, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_Start_InvalidPayload", 1, nil)
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
	mockWorker.AssertNotCalled(t, "ProcessTask")
}

func TestWithChannelBufferSize(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestWithChannelBufferSize", 1, nil, WithChannelBufferSize(200))
	assert.NoError(t, err)

	// Verify the consumer was created with custom buffer size
	nc := c.(*natsConsumer)
	assert.Equal(t, 200, nc.channelBufSize)
}

func TestWithChannelBufferSize_Default(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestWithChannelBufferSize_Default", 1, nil)
	assert.NoError(t, err)

	nc := c.(*natsConsumer)
	assert.Equal(t, 100, nc.channelBufSize) // Default value
}

func TestNewTaskConsumerFromJS_DefaultNumWorkers(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	// Test with numWorkers = 0, should default to 16
	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestNewTaskConsumerFromJS_DefaultNumWorkers", 0, nil)
	assert.NoError(t, err)

	nc := c.(*natsConsumer)
	assert.Equal(t, 16, nc.numWorkers) // Default value

	// Test with numWorkers = -1, should also default to 16
	c2, err := NewTaskConsumerFromJS(js, mockWorker, "TestNewTaskConsumerFromJS_DefaultNumWorkers", -1, nil)
	assert.NoError(t, err)

	nc2 := c2.(*natsConsumer)
	assert.Equal(t, 16, nc2.numWorkers) // Default value
}

func TestWithDrainTimeout(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestWithDrainTimeout", 1, nil, WithDrainTimeout(10*time.Second))
	assert.NoError(t, err)

	nc := c.(*natsConsumer)
	assert.Equal(t, 10*time.Second, nc.drainTimeout)
}

func TestWithDrainTimeout_Default(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestWithDrainTimeout_Default", 1, nil)
	assert.NoError(t, err)

	nc := c.(*natsConsumer)
	assert.Equal(t, 5*time.Second, nc.drainTimeout) // Default value
}

func TestWithShutdownTimeout(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestWithShutdownTimeout", 1, nil, WithShutdownTimeout(20*time.Second))
	assert.NoError(t, err)

	nc := c.(*natsConsumer)
	assert.Equal(t, 20*time.Second, nc.shutdownTimeout)
}

func TestWithShutdownTimeout_Default(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	c, err := NewTaskConsumerFromJS(js, mockWorker, "TestWithShutdownTimeout_Default", 1, nil)
	assert.NoError(t, err)

	nc := c.(*natsConsumer)
	assert.Equal(t, 10*time.Second, nc.shutdownTimeout) // Default value
}

func TestConsumer_GracefulShutdown_DrainComplete(t *testing.T) {
	t.Parallel()
	// Test that all messages are processed during graceful shutdown
	js := new(MockJetStream)
	mockWorker := new(MockWorker)
	stream := new(MockStream)
	consumer := new(MockConsumer)

	c, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_GracefulShutdown_DrainComplete", 1, nil,
		WithDrainTimeout(100*time.Millisecond),
		WithShutdownTimeout(200*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())

	processedCount := 0

	// Expectations
	js.On("CreateOrUpdateStream", ctx, mock.Anything).Return(stream, nil)
	js.On("CreateOrUpdateConsumer", ctx, mock.Anything, mock.Anything).Return(consumer, nil)

	consumeCtx := new(MockConsumeContext)
	consumer.On("Consume", mock.Anything).Return(consumeCtx, nil).Run(func(args mock.Arguments) {
		handler := args.Get(0).(jetstream.MessageHandler)

		// Send multiple messages then cancel
		go func() {
			for i := 0; i < 3; i++ {
				msg := new(MockMsg)
				task := trigger.DeliveryTask{TriggerID: "t" + string(rune('1'+i))}
				taskBytes, _ := json.Marshal(task)
				msg.On("Data").Return(taskBytes)
				msg.On("Ack").Return(nil)
				handler(msg)
			}
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
	})
	consumeCtx.On("Stop").Return()

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		processedCount++
	})

	err := c.Start(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 3, processedCount, "All messages should be processed")
}

func TestConsumer_GracefulShutdown_MessageNakOnClose(t *testing.T) {
	t.Parallel()
	// Test that messages received during closing are NAK'd
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	nc, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_GracefulShutdown_MessageNakOnClose", 1, nil)
	consumer := nc.(*natsConsumer)

	// Set closing state
	consumer.closing.Store(true)

	// Create a mock message
	msg := new(MockMsg)
	task := trigger.DeliveryTask{TriggerID: "t1"}
	taskBytes, _ := json.Marshal(task)
	msg.On("Data").Return(taskBytes)
	msg.On("Nak").Return(nil)

	// Call dispatch directly
	consumer.dispatch(msg)

	// Verify NAK was called
	msg.AssertCalled(t, "Nak")
	// Worker should NOT be called
	mockWorker.AssertNotCalled(t, "ProcessTask")
}

func TestConsumer_InFlightCount(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	nc, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_InFlightCount", 1, nil)
	consumer := nc.(*natsConsumer)

	// Initialize worker channels
	consumer.workerChans = make([]chan jetstream.Msg, 1)
	consumer.workerChans[0] = make(chan jetstream.Msg, 10)

	assert.Equal(t, int32(0), consumer.inFlightCount.Load())

	// Start dispatch in a goroutine
	done := make(chan struct{})
	msg := new(MockMsg)
	task := trigger.DeliveryTask{TriggerID: "t1", Collection: "col1", DocumentID: "doc1"}
	taskBytes, _ := json.Marshal(task)
	msg.On("Data").Return(taskBytes)

	go func() {
		consumer.dispatch(msg)
		close(done)
	}()

	// Wait for dispatch to complete
	<-done

	// After dispatch completes, in-flight count should be back to 0
	assert.Equal(t, int32(0), consumer.inFlightCount.Load())

	// Drain the channel
	<-consumer.workerChans[0]
}

func TestConsumer_WaitForDrain(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	nc, _ := NewTaskConsumerFromJS(js, mockWorker, "TestConsumer_WaitForDrain", 1, nil)
	consumer := nc.(*natsConsumer)

	// Test immediate drain (no in-flight messages)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	start := time.Now()
	consumer.waitForDrain(ctx)
	elapsed := time.Since(start)

	// Should return quickly (< 200ms)
	assert.Less(t, elapsed, 200*time.Millisecond, "waitForDrain should return quickly when no in-flight messages")
}

func TestConsumer_WaitForDrain_Timeout(t *testing.T) {
	t.Parallel()
	js := new(MockJetStream)
	mockWorker := new(MockWorker)

	nc, _ := NewTaskConsumerFromJS(js, mockWorker, "WaitForDrain_Timeout", 1, nil)
	consumer := nc.(*natsConsumer)

	// Simulate in-flight messages
	consumer.inFlightCount.Store(5)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	consumer.waitForDrain(ctx)
	elapsed := time.Since(start)

	// Should timeout after ~100ms
	assert.GreaterOrEqual(t, elapsed, 95*time.Millisecond, "waitForDrain should timeout")
	assert.Less(t, elapsed, 300*time.Millisecond, "waitForDrain should not exceed timeout by much")
}

func TestNewTaskConsumerFromJS_EmptyStream(t *testing.T) {
	mockJS := new(MockJetStream)
	mockWorker := new(MockWorker)

	// Mock CreateOrUpdateConsumer
	mockJS.On("CreateOrUpdateConsumer", mock.Anything, "TRIGGERS", mock.Anything).Return(new(MockConsumer), nil)

	consumer, err := NewTaskConsumerFromJS(mockJS, mockWorker, "", 1, nil)
	assert.NoError(t, err)
	assert.NotNil(t, consumer)
}
