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
	"github.com/stretchr/testify/require"
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
	mgr.rtServer = realtime.NewServer(stub, cfg.Storage.Document.Mongo.DataCollection)

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)
	time.Sleep(120 * time.Millisecond)
	bgCancel()

	assert.GreaterOrEqual(t, stub.calls.Load(), int32(1))
}

func TestManager_Start_RealtimeBackground_Success(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunAPI: true})
	stub := &rtQueryStub{}
	mgr.rtServer = realtime.NewServer(stub, cfg.Storage.Document.Mongo.DataCollection)

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)
	time.Sleep(80 * time.Millisecond)

	assert.Equal(t, int32(1), stub.calls.Load())
}

func TestManager_Start_RealtimeBackground_RetryThenSuccess(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunAPI: true})
	stub := &rtQueryStub{failFirst: true}
	mgr.rtServer = realtime.NewServer(stub, cfg.Storage.Document.Mongo.DataCollection)

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	mgr.Start(bgCtx)
	time.Sleep(140 * time.Millisecond)

	assert.Equal(t, int32(2), stub.calls.Load())
}

type storageBackendStub struct {
	watchCalls atomic.Int32
}

func (s *storageBackendStub) Get(context.Context, string) (*storage.Document, error) {
	return nil, model.ErrNotFound
}
func (s *storageBackendStub) Create(context.Context, *storage.Document) error { return nil }
func (s *storageBackendStub) Update(context.Context, string, map[string]interface{}, model.Filters) error {
	return nil
}
func (s *storageBackendStub) Patch(context.Context, string, map[string]interface{}, model.Filters) error {
	return nil
}
func (s *storageBackendStub) Delete(context.Context, string, model.Filters) error { return nil }
func (s *storageBackendStub) Query(context.Context, model.Query) ([]*storage.Document, error) {
	return nil, nil
}
func (s *storageBackendStub) Watch(context.Context, string, interface{}, storage.WatchOptions) (<-chan storage.Event, error) {
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

func TestManager_Start_TriggerEvaluator_CallsWatch(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerEvaluator: true})

	backend := &storageBackendStub{}
	mgr.docProvider = &fakeDocumentProvider{store: backend}
	mgr.triggerService = trigger.NewTriggerService(&fakeEvaluator{}, &fakePublisher{})

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(bgCtx)

	time.Sleep(30 * time.Millisecond)
	require.Equal(t, int32(1), backend.watchCalls.Load())
}

func TestManager_Start_TriggerWorker_CallsStart(t *testing.T) {
	cfg := config.LoadConfig()
	mgr := NewManager(cfg, Options{RunTriggerWorker: true})

	worker := &triggerConsumerStub{}
	mgr.triggerConsumer = worker

	bgCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(bgCtx)

	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(1), worker.called.Load())
}

type rtQueryStub struct {
	calls      atomic.Int32
	failFirst  bool
	failAlways bool
}

func (s *rtQueryStub) GetDocument(context.Context, string) (model.Document, error) {
	return model.Document{}, nil
}
func (s *rtQueryStub) CreateDocument(context.Context, model.Document) error { return nil }
func (s *rtQueryStub) ReplaceDocument(context.Context, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}
func (s *rtQueryStub) PatchDocument(context.Context, model.Document, model.Filters) (model.Document, error) {
	return model.Document{}, nil
}
func (s *rtQueryStub) DeleteDocument(context.Context, string, model.Filters) error {
	return nil
}
func (s *rtQueryStub) ExecuteQuery(context.Context, model.Query) ([]model.Document, error) {
	return nil, nil
}
func (s *rtQueryStub) WatchCollection(context.Context, string) (<-chan storage.Event, error) {
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
func (s *rtQueryStub) Pull(context.Context, storage.ReplicationPullRequest) (*storage.ReplicationPullResponse, error) {
	return nil, nil
}
func (s *rtQueryStub) Push(context.Context, storage.ReplicationPushRequest) (*storage.ReplicationPushResponse, error) {
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
