package pubsub

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockJetStream struct {
mock.Mock
jetstream.JetStream
}

func (m *MockJetStream) Publish(ctx context.Context, subject string, data []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
args := m.Called(ctx, subject, data, opts)
if args.Get(0) == nil {
return nil, args.Error(1)
}
return args.Get(0).(*jetstream.PubAck), args.Error(1)
}

func TestNatsPublisher_Publish(t *testing.T) {
	mockJS := new(MockJetStream)
	publisher := NewTaskPublisherFromJS(mockJS, nil)

	task := &trigger.DeliveryTask{
Tenant:     "acme",
Collection: "users",
DocKey:     "user-1",
TriggerID:  "t1",
}

	expectedSubject := "triggers.acme.users.dXNlci0x" // base64url("user-1")
expectedData, _ := json.Marshal(task)

mockJS.On("Publish", mock.Anything, expectedSubject, expectedData, mock.Anything).Return(&jetstream.PubAck{}, nil)

err := publisher.Publish(context.Background(), task)
assert.NoError(t, err)

mockJS.AssertExpectations(t)
}
func TestNatsPublisher_Publish_HashedSubject(t *testing.T) {
	mockJS := new(MockJetStream)
	publisher := NewTaskPublisherFromJS(mockJS, nil)

	// Create a long DocKey to force hashing
	longDocKey := strings.Repeat("a", 1000)
	encodedDocKey := base64.URLEncoding.EncodeToString([]byte(longDocKey))

	task := &trigger.DeliveryTask{
		Tenant:     "acme",
		Collection: "users",
		DocKey:     longDocKey,
		TriggerID:  "t1",
	}

	// Calculate expected subject
	originalSubject := fmt.Sprintf("triggers.%s.%s.%s", task.Tenant, task.Collection, encodedDocKey)
	// Verify it is indeed long enough
	assert.True(t, len(originalSubject) > 1024)

	hash := sha256.Sum256([]byte(originalSubject))
	hashStr := hex.EncodeToString(hash[:16])
	expectedSubject := fmt.Sprintf("triggers.hashed.%s", hashStr)

	// The task passed to Publish will be modified (SubjectHashed = true)
	// So we need to match the data argument with SubjectHashed = true
	expectedTask := *task
	expectedTask.SubjectHashed = true
	expectedData, _ := json.Marshal(&expectedTask)

	mockJS.On("Publish", mock.Anything, expectedSubject, expectedData, mock.Anything).Return(&jetstream.PubAck{}, nil)

	err := publisher.Publish(context.Background(), task)
	assert.NoError(t, err)
	assert.True(t, task.SubjectHashed) // Verify the task was modified in place

	mockJS.AssertExpectations(t)
}
func TestNatsPublisher_Publish_Error(t *testing.T) {
mockJS := new(MockJetStream)
publisher := NewTaskPublisherFromJS(mockJS, nil)

task := &trigger.DeliveryTask{
Tenant:     "acme",
Collection: "users",
DocKey:     "user-1",
TriggerID:  "t1",
}

mockJS.On("Publish", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, assert.AnError)

err := publisher.Publish(context.Background(), task)
assert.Error(t, err)

mockJS.AssertExpectations(t)
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
