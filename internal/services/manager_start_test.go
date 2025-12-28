package services

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/api/realtime"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/storage"
	"github.com/codetrek/syntrix/internal/trigger"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestManager_Start_Shutdown_WithServer(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{})

	calls := atomic.Int32{}
	addr := freeAddr()
	server := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})}
	mgr.servers = append(mgr.servers, server)
	mgr.serverNames = append(mgr.serverNames, "test-server")

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)
	assert.NoError(t, waitForServer(addr, 500*time.Millisecond))

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()

	mgr.Shutdown(shutdownCtx)

	err := waitForServer(addr, 200*time.Millisecond)
	assert.Error(t, err)
	assert.GreaterOrEqual(t, calls.Load(), int32(1))
}

func TestManager_Start_RealtimeBackground_Failure(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunAPI: true})
	stub := &rtQueryStub{failAlways: true}
	mgr.rtServer = realtime.NewServer(stub, cfg.Storage.Topology.Document.DataCollection, nil, realtime.Config{})

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)

	assert.Eventually(t, func() bool {
		return stub.calls.Load() >= 1
	}, 1*time.Second, 10*time.Millisecond, "Should retry and call stub at least once")

	bgCancel()
}

func TestManager_Start_RealtimeBackground_Success(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunAPI: true})
	stub := &rtQueryStub{}
	mgr.rtServer = realtime.NewServer(stub, cfg.Storage.Topology.Document.DataCollection, nil, realtime.Config{})

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)

	assert.Eventually(t, func() bool {
		return stub.calls.Load() == 1
	}, 1*time.Second, 10*time.Millisecond, "Should call stub exactly once")
}

func TestManager_Start_RealtimeBackground_RetryThenSuccess(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunAPI: true})
	stub := &rtQueryStub{failFirst: true}
	mgr.rtServer = realtime.NewServer(stub, cfg.Storage.Topology.Document.DataCollection, nil, realtime.Config{})

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)

	assert.Eventually(t, func() bool {
		return stub.calls.Load() == 2
	}, 1*time.Second, 10*time.Millisecond, "Should call stub twice (retry then success)")
}

type storageBackendStub struct {
	watchCalls atomic.Int32
}

func (s *storageBackendStub) Get(context.Context, string, string) (*storage.Document, error) {
	return nil, model.ErrNotFound
}
func (s *storageBackendStub) Create(context.Context, string, *storage.Document) error { return nil }
func (s *storageBackendStub) Update(context.Context, string, string, map[string]interface{}, model.Filters) error {
	return nil
}
func (s *storageBackendStub) Patch(context.Context, string, string, map[string]interface{}, model.Filters) error {
	return nil
}
func (s *storageBackendStub) Delete(context.Context, string, string, model.Filters) error { return nil }
func (s *storageBackendStub) Query(context.Context, string, model.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (s *storageBackendStub) Watch(context.Context, string, string, interface{}, storage.WatchOptions) (<-chan storage.Event, error) {
	s.watchCalls.Add(1)
	ch := make(chan storage.Event)
	close(ch)
	return ch, nil
}
func (s *storageBackendStub) Close(context.Context) error { return nil }
func (s *storageBackendStub) DB() *mongo.Database         { return nil }

type triggerConsumerStub struct {
	called atomic.Int32
}

func (c *triggerConsumerStub) Start(ctx context.Context) error {
	c.called.Add(1)
	<-ctx.Done()
	return nil
}

type mockTriggerService struct {
	mock.Mock
}

func (m *mockTriggerService) Start(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *mockTriggerService) LoadTriggers(triggers []*trigger.Trigger) error {
	args := m.Called(triggers)
	return args.Error(0)
}

func TestManager_Start_TriggerEvaluator_CallsStart(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	mockTS := new(mockTriggerService)
	mgr.triggerService = mockTS

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockTS.On("Start", bgCtx).Return(nil)

	mgr.Start(bgCtx)

	// Wait for Start to be called
	time.Sleep(50 * time.Millisecond)
	mockTS.AssertExpectations(t)
}

func TestManager_Start_TriggerWorker_CallsStart(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	worker := &triggerConsumerStub{}
	mgr.triggerConsumer = worker

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(bgCtx)

	assert.Eventually(t, func() bool {
		return worker.called.Load() == 1
	}, 1*time.Second, 10*time.Millisecond, "Should call Start exactly once")
}

type rtQueryStub struct {
	calls      atomic.Int32
	failFirst  bool
	failAlways bool
}

func (s *rtQueryStub) GetDocument(context.Context, string, string) (model.Document, error) {
	return model.Document{}, nil
}
func (s *rtQueryStub) CreateDocument(context.Context, string, model.Document) error { return nil }
func (s *rtQueryStub) ReplaceDocument(context.Context, string, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}
func (s *rtQueryStub) PatchDocument(context.Context, string, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}
func (s *rtQueryStub) DeleteDocument(context.Context, string, string, model.Filters) error {
	return nil
}
func (s *rtQueryStub) ExecuteQuery(context.Context, string, model.Query) ([]model.Document, error) {
	return nil, nil
}
func (s *rtQueryStub) WatchCollection(context.Context, string, string) (<-chan storage.Event, error) {
	if s.failAlways {
		s.calls.Add(1)
		return nil, errors.New("watch fail")
	}

	if s.calls.Add(1) == 1 && s.failFirst {
		return nil, errors.New("watch fail")
	}
	ch := make(chan storage.Event)
	close(ch)
	return ch, nil
}
func (s *rtQueryStub) Pull(context.Context, string, storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}
func (s *rtQueryStub) Push(context.Context, string, storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
	return nil, nil
}

func freeAddr() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "127.0.0.1:0"
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func waitForServer(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 100 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("server not reachable")
}
