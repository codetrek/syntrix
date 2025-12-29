package engine

import (
	"context"

	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/codetrek/syntrix/internal/trigger/types"
	"github.com/stretchr/testify/mock"
)

// MockEvaluator
type MockEvaluator struct {
	mock.Mock
}

func (m *MockEvaluator) Evaluate(ctx context.Context, t *trigger.Trigger, event *storage.Event) (bool, error) {
	args := m.Called(ctx, t, event)
	return args.Bool(0), args.Error(1)
}

// MockWatcher
type MockWatcher struct {
	mock.Mock
}

func (m *MockWatcher) Watch(ctx context.Context) (<-chan storage.Event, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(<-chan storage.Event), args.Error(1)
}

func (m *MockWatcher) SaveCheckpoint(ctx context.Context, token interface{}) error {
	args := m.Called(ctx, token)
	return args.Error(0)
}

func (m *MockWatcher) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockPublisher
type MockPublisher struct {
	mock.Mock
}

func (m *MockPublisher) Publish(ctx context.Context, task *types.DeliveryTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
}

func (m *MockPublisher) Close() error {
	args := m.Called()
	return args.Error(0)
}
