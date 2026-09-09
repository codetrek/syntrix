package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/pubsub"
	pubsubtesting "github.com/syntrixbase/syntrix/internal/core/pubsub/testing"
	"github.com/syntrixbase/syntrix/internal/trigger/delivery/worker"
	"github.com/syntrixbase/syntrix/internal/trigger/types"
)

func TestConsumer_Worker_HTTPResponses(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []int
		nakDelays []time.Duration
		wantAck   bool
		wantTerm  bool
	}{
		{
			name:      "rate_limit_then_success",
			statuses:  []int{http.StatusTooManyRequests, http.StatusNoContent},
			nakDelays: []time.Duration{2 * time.Second, 0},
			wantAck:   true,
		},
		{
			name:      "rate_limit_exhaustion",
			statuses:  []int{http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusTooManyRequests},
			nakDelays: []time.Duration{2 * time.Second, 3 * time.Second, 0},
			wantTerm:  true,
		},
		{
			name:      "bad_request",
			statuses:  []int{http.StatusBadRequest},
			nakDelays: []time.Duration{0},
			wantTerm:  true,
		},
		{
			name:      "server_error",
			statuses:  []int{http.StatusInternalServerError},
			nakDelays: []time.Duration{2 * time.Second},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := int(requests.Add(1))
				if attempt > len(tt.statuses) {
					t.Errorf("unexpected HTTP request %d", attempt)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(tt.statuses[attempt-1])
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			workerMetrics := new(MockMetrics)
			workerMetrics.Test(t)
			for _, status := range tt.statuses {
				switch status {
				case http.StatusNoContent:
					workerMetrics.On("IncDeliverySuccess", "database1", "users").Once()
					workerMetrics.On("ObserveDeliveryLatency", "database1", "users", mock.Anything).Once()
				case http.StatusBadRequest:
					workerMetrics.On("IncDeliveryFailure", "database1", "users", status, true).Once()
				default:
					workerMetrics.On("IncDeliveryFailure", "database1", "users", status, false).Once()
				}
			}
			c := &natsConsumer{
				worker:     worker.NewDeliveryWorker(nil, nil, worker.HTTPClientOptions{}, workerMetrics),
				numWorkers: 1,
				metrics:    &types.NoopMetrics{},
			}
			data, err := json.Marshal(&types.DeliveryTask{
				TriggerID:  "http-response-trigger",
				Database:   "database1",
				Collection: "users",
				URL:        server.URL,
				RetryPolicy: types.RetryPolicy{
					MaxAttempts:    3,
					InitialBackoff: types.Duration(2 * time.Second),
					MaxBackoff:     types.Duration(3 * time.Second),
				},
			})
			require.NoError(t, err)

			for i := range tt.statuses {
				// The mock records the requested delay; each new message simulates the next broker delivery.
				msg := pubsubtesting.NewMockMessage("test.subject", data)
				msg.SetMetadata(pubsub.MessageMetadata{NumDelivered: uint64(i + 1)})
				c.workerChans = []chan pubsub.Message{make(chan pubsub.Message, 1)}
				c.workerChans[0] <- msg
				close(c.workerChans[0])
				c.wg.Add(1)
				c.workerLoop(ctx, 0)

				last := i == len(tt.statuses)-1
				require.Equal(t, tt.nakDelays[i] > 0, msg.IsNaked(), "delivery %d", i+1)
				require.Equal(t, tt.nakDelays[i], msg.NakDelay(), "delivery %d", i+1)
				require.Equal(t, last && tt.wantAck, msg.IsAcked(), "delivery %d", i+1)
				require.Equal(t, last && tt.wantTerm, msg.IsTermed(), "delivery %d", i+1)
				require.Equal(t, int32(i+1), requests.Load())
			}
			workerMetrics.AssertExpectations(t)
		})
	}
}

// TestConsumer_Dispatch_InvalidPayload verifies that invalid payloads are Terminated.
func TestConsumer_Dispatch_InvalidPayload(t *testing.T) {
	// Setup
	c := &natsConsumer{
		numWorkers:  1,
		workerChans: []chan pubsub.Message{make(chan pubsub.Message, 1)},
		metrics:     &types.NoopMetrics{},
	}

	msg := pubsubtesting.NewMockMessage("test.subject", []byte("invalid-json"))

	// Execute
	c.dispatch(msg)

	// Verify - invalid payload should be terminated
	assert.True(t, msg.IsTermed())
}

// TestConsumer_Worker_RetryLogic verifies the retry logic in workerLoop.
func TestConsumer_Worker_RetryLogic(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		processErr     error
		metadataErr    error
		numDelivered   uint64
		maxAttempts    int
		initialBackoff time.Duration
		maxBackoff     time.Duration
		payload        interface{} // string (invalid) or DeliveryTask
		expectTerm     bool
		expectNak      bool
		expectNakDelay time.Duration
		expectAck      bool
	}{
		{
			name:       "Success",
			processErr: nil,
			payload: &types.DeliveryTask{
				TriggerID: "t1",
			},
			expectAck: true,
		},
		{
			name:         "ProcessError_Retry",
			processErr:   errors.New("fail"),
			numDelivered: 1,
			maxAttempts:  3,
			payload: &types.DeliveryTask{
				TriggerID: "t1",
				RetryPolicy: types.RetryPolicy{
					MaxAttempts:    3,
					InitialBackoff: types.Duration(1 * time.Second),
				},
			},
			expectNak:      true,
			expectNakDelay: 1 * time.Second,
		},
		{
			name:         "ProcessError_MaxAttemptsReached",
			processErr:   errors.New("fail"),
			numDelivered: 3,
			maxAttempts:  3,
			payload: &types.DeliveryTask{
				TriggerID: "t1",
				RetryPolicy: types.RetryPolicy{
					MaxAttempts: 3,
				},
			},
			expectTerm: true,
		},
		{
			name:         "ProcessError_Fatal",
			processErr:   &types.FatalError{Err: errors.New("fatal")},
			numDelivered: 1,
			maxAttempts:  3,
			payload: &types.DeliveryTask{
				TriggerID: "t1",
			},
			expectTerm: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockWorker := new(MockWorker)
			c := &natsConsumer{
				worker:      mockWorker,
				numWorkers:  1,
				workerChans: []chan pubsub.Message{make(chan pubsub.Message, 1)},
				metrics:     &types.NoopMetrics{},
			}
			c.wg.Add(1)

			var msg *pubsubtesting.MockMessage

			// Setup expectations
			if tt.payload != nil {
				if task, ok := tt.payload.(*types.DeliveryTask); ok {
					data, _ := json.Marshal(task)
					msg = pubsubtesting.NewMockMessage("test.subject", data)
					msg.SetMetadata(pubsub.MessageMetadata{NumDelivered: tt.numDelivered})

					if tt.processErr != nil {
						mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(tt.processErr)
					} else {
						mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(nil)
					}
				}
			}

			// Inject message
			c.workerChans[0] <- msg
			close(c.workerChans[0])

			// Run worker loop
			c.workerLoop(context.Background(), 0)

			// Verify
			if tt.expectAck {
				assert.True(t, msg.IsAcked())
			}
			if tt.expectTerm {
				assert.True(t, msg.IsTermed())
			}
			if tt.expectNak {
				assert.True(t, msg.IsNaked())
				assert.Equal(t, tt.expectNakDelay, msg.NakDelay())
			}
			mockWorker.AssertExpectations(t)
		})
	}
}

// TestConsumer_Worker_MetadataError verifies handling of metadata retrieval errors.
func TestConsumer_Worker_MetadataError(t *testing.T) {
	mockWorker := new(MockWorker)
	c := &natsConsumer{
		worker:      mockWorker,
		numWorkers:  1,
		workerChans: []chan pubsub.Message{make(chan pubsub.Message, 1)},
		metrics:     &types.NoopMetrics{},
	}
	c.wg.Add(1)

	task := &types.DeliveryTask{TriggerID: "t1"}
	data, _ := json.Marshal(task)
	msg := pubsubtesting.NewMockMessage("test.subject", data)
	msg.SetErrors(nil, nil, nil, errors.New("metadata error"))

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("process failed"))

	c.workerChans[0] <- msg
	close(c.workerChans[0])

	c.workerLoop(context.Background(), 0)

	// On metadata error, should Nak without delay
	assert.True(t, msg.IsNaked())
	assert.Equal(t, time.Duration(0), msg.NakDelay())
	mockWorker.AssertExpectations(t)
}

// TestConsumer_Worker_MaxBackoffLimit verifies that backoff is capped at maxBackoff.
func TestConsumer_Worker_MaxBackoffLimit(t *testing.T) {
	mockWorker := new(MockWorker)
	c := &natsConsumer{
		worker:      mockWorker,
		numWorkers:  1,
		workerChans: []chan pubsub.Message{make(chan pubsub.Message, 1)},
		metrics:     &types.NoopMetrics{},
	}
	c.wg.Add(1)

	task := &types.DeliveryTask{
		TriggerID: "t1",
		RetryPolicy: types.RetryPolicy{
			MaxAttempts:    10,
			InitialBackoff: types.Duration(1 * time.Second),
			MaxBackoff:     types.Duration(5 * time.Second), // Cap at 5s
		},
	}
	data, _ := json.Marshal(task)
	msg := pubsubtesting.NewMockMessage("test.subject", data)
	// NumDelivered=5 means attempt 5, exponential backoff would be 16s, but capped at 5s
	msg.SetMetadata(pubsub.MessageMetadata{NumDelivered: 5})

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("process failed"))

	c.workerChans[0] <- msg
	close(c.workerChans[0])

	c.workerLoop(context.Background(), 0)

	assert.True(t, msg.IsNaked())
	assert.Equal(t, 5*time.Second, msg.NakDelay())
	mockWorker.AssertExpectations(t)
}

// TestConsumer_Worker_DefaultRetryValues verifies default retry values are used when not specified.
func TestConsumer_Worker_DefaultRetryValues(t *testing.T) {
	mockWorker := new(MockWorker)
	c := &natsConsumer{
		worker:      mockWorker,
		numWorkers:  1,
		workerChans: []chan pubsub.Message{make(chan pubsub.Message, 1)},
		metrics:     &types.NoopMetrics{},
	}
	c.wg.Add(1)

	// Task with no retry policy - should use defaults (maxAttempts=3, initialBackoff=1s)
	task := &types.DeliveryTask{
		TriggerID: "t1",
	}
	data, _ := json.Marshal(task)
	msg := pubsubtesting.NewMockMessage("test.subject", data)
	msg.SetMetadata(pubsub.MessageMetadata{NumDelivered: 1})

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("process failed"))

	c.workerChans[0] <- msg
	close(c.workerChans[0])

	c.workerLoop(context.Background(), 0)

	assert.True(t, msg.IsNaked())
	assert.Equal(t, 1*time.Second, msg.NakDelay()) // Default initial backoff
	mockWorker.AssertExpectations(t)
}

// TestConsumer_ProcessMsg_WithTimeout verifies that custom timeout is applied.
func TestConsumer_ProcessMsg_WithTimeout(t *testing.T) {
	mockWorker := new(MockWorker)
	c := &natsConsumer{
		worker:  mockWorker,
		metrics: &types.NoopMetrics{},
	}

	task := &types.DeliveryTask{
		TriggerID:  "t1",
		Database:   "database1",
		Collection: "col1",
		Timeout:    types.Duration(500 * time.Millisecond), // Custom timeout
	}
	data, _ := json.Marshal(task)
	msg := pubsubtesting.NewMockMessage("test.subject", data)

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(nil)

	err := c.processMsg(context.Background(), msg)
	assert.NoError(t, err)

	mockWorker.AssertExpectations(t)
}

// TestConsumer_ProcessMsg_InvalidPayload verifies error handling for invalid payload.
func TestConsumer_ProcessMsg_InvalidPayload(t *testing.T) {
	mockWorker := new(MockWorker)
	c := &natsConsumer{
		worker:  mockWorker,
		metrics: &types.NoopMetrics{},
	}

	msg := pubsubtesting.NewMockMessage("test.subject", []byte("invalid-json"))

	err := c.processMsg(context.Background(), msg)
	assert.ErrorContains(t, err, "invalid payload")
}

// TestConsumer_ProcessMsg_WorkerError verifies metrics are recorded on worker error.
func TestConsumer_ProcessMsg_WorkerError(t *testing.T) {
	mockWorker := new(MockWorker)
	mockMetrics := new(MockMetrics)
	c := &natsConsumer{
		worker:  mockWorker,
		metrics: mockMetrics,
	}

	task := &types.DeliveryTask{
		TriggerID:  "t1",
		Database:   "database1",
		Collection: "col1",
	}
	data, _ := json.Marshal(task)
	msg := pubsubtesting.NewMockMessage("test.subject", data)

	mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(errors.New("worker failed"))
	mockMetrics.On("IncConsumeFailure", "database1", "col1", "worker failed").Return()
	mockMetrics.On("ObserveConsumeLatency", "database1", "col1", mock.Anything).Return()

	err := c.processMsg(context.Background(), msg)
	assert.Error(t, err)

	mockWorker.AssertExpectations(t)
	mockMetrics.AssertExpectations(t)
}

// MockMetrics is a mock implementation of types.Metrics for testing.
type MockMetrics struct {
	mock.Mock
}

func (m *MockMetrics) IncPublishSuccess(database, collection string, hashed bool) {
	m.Called(database, collection, hashed)
}

func (m *MockMetrics) IncPublishFailure(database, collection, reason string) {
	m.Called(database, collection, reason)
}

func (m *MockMetrics) IncConsumeSuccess(database, collection string, hashed bool) {
	m.Called(database, collection, hashed)
}

func (m *MockMetrics) IncConsumeFailure(database, collection, reason string) {
	m.Called(database, collection, reason)
}

func (m *MockMetrics) ObservePublishLatency(database, collection string, d time.Duration) {
	m.Called(database, collection, d)
}

func (m *MockMetrics) ObserveConsumeLatency(database, collection string, d time.Duration) {
	m.Called(database, collection, d)
}

func (m *MockMetrics) IncHashCollision(database, collection string) {
	m.Called(database, collection)
}

func (m *MockMetrics) IncDeliverySuccess(database, collection string) {
	m.Called(database, collection)
}

func (m *MockMetrics) IncDeliveryFailure(database, collection string, status int, fatal bool) {
	m.Called(database, collection, status, fatal)
}

func (m *MockMetrics) IncDeliveryRetry(database, collection string) {
	m.Called(database, collection)
}

func (m *MockMetrics) ObserveDeliveryLatency(database, collection string, d time.Duration) {
	m.Called(database, collection, d)
}
