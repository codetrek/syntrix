package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/mock"
)

// TestConsumer_Dispatch_InvalidPayload verifies that invalid payloads are Terminated.
func TestConsumer_Dispatch_InvalidPayload(t *testing.T) {
	// Setup
	c := &natsConsumer{
		numWorkers:  1,
		workerChans: []chan jetstream.Msg{make(chan jetstream.Msg, 1)},
		metrics:     &types.NoopMetrics{},
	}
	msg := new(MockMsg)

	// Expectations
	msg.On("Data").Return([]byte("invalid-json"))
	msg.On("Term").Return(nil)

	// Execute
	c.dispatch(msg)

	// Verify
	msg.AssertExpectations(t)
}

// TestConsumer_Worker_RetryLogic verifies the retry logic in workerLoop.
func TestConsumer_Worker_RetryLogic(t *testing.T) {
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
	}{
		{
			name:       "Success",
			processErr: nil,
			payload: &types.DeliveryTask{
				TriggerID: "t1",
			},
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
			mockWorker := new(MockWorker)
			c := &natsConsumer{
				worker:      mockWorker,
				numWorkers:  1,
				workerChans: []chan jetstream.Msg{make(chan jetstream.Msg, 1)},
				metrics:     &types.NoopMetrics{},
			}
			c.wg.Add(1)

			msg := new(MockMsg)

			// Setup expectations
			if tt.payload != nil {
				if task, ok := tt.payload.(*types.DeliveryTask); ok {
					data, _ := json.Marshal(task)
					msg.On("Data").Return(data)
					if tt.processErr != nil {
						mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(tt.processErr)
					} else {
						mockWorker.On("ProcessTask", mock.Anything, mock.Anything).Return(nil)
						msg.On("Ack").Return(nil)
					}
				} else {
					// Invalid payload string
					msg.On("Data").Return([]byte(tt.payload.(string)))
				}
			}

			if tt.processErr != nil {
				if !types.IsFatal(tt.processErr) {
					md := &jetstream.MsgMetadata{NumDelivered: tt.numDelivered}
					msg.On("Metadata").Return(md, tt.metadataErr)
				}
				if tt.expectTerm {
					msg.On("Term").Return(nil)
				} else if tt.expectNak {
					msg.On("NakWithDelay", tt.expectNakDelay).Return(nil)
				}
			}

			// Inject message
			c.workerChans[0] <- msg
			close(c.workerChans[0])

			// Run worker loop
			c.workerLoop(context.Background(), 0)

			// Verify
			msg.AssertExpectations(t)
			mockWorker.AssertExpectations(t)
		})
	}
}
