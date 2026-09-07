package core

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var testTokenCounter atomic.Uint64

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func newTestConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Buffer.Path = t.TempDir()
	cfg.Buffer.BatchSize = 1
	cfg.Buffer.BatchInterval = time.Hour
	cfg.Buffer.QueueSize = 100
	cfg.Buffer.BatchBytes = 1 << 20
	cfg.Buffer.QueueBytes = 2 << 20
	cfg.Consumer.PageSize = 2
	cfg.Consumer.PageBytes = 1 << 20
	cfg.Consumer.QueueBytes = 2 << 20
	cfg.GRPC.ChannelSize = 100
	cfg.Cleaner.Interval = time.Hour
	return cfg
}

func newTestPuller(t *testing.T, cfg config.Config, names ...string) *Puller {
	t.Helper()
	p := New(cfg, nil)
	p.currentTimestamp = func(context.Context, *mongo.Client) (primitive.Timestamp, error) {
		return primitive.Timestamp{T: 100, I: 1}, nil
	}
	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://localhost:27017"))
	require.NoError(t, err)
	for _, name := range names {
		require.NoError(t, p.AddBackend(name, client, "test", config.PullerBackendConfig{Name: name, SourceID: name + "-source", Collections: []string{"documents"}}))
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := p.Stop(ctx)
		if p.Err() == nil {
			require.NoError(t, err)
		}
	})
	return p
}

func testEvent(id int) *events.StoreChangeEvent {
	return &events.StoreChangeEvent{MgoColl: "documents", MgoDocID: fmt.Sprint(id), OpType: events.StoreOperationInsert,
		ClusterTime: events.ClusterTime{T: 100, I: 1}, Timestamp: time.Now().UnixMilli()}
}

func enqueueEvents(t *testing.T, p *Puller, name string, evts ...*events.StoreChangeEvent) {
	t.Helper()
	for _, evt := range evts {
		token, err := bson.Marshal(bson.D{{Key: "token", Value: int64(testTokenCounter.Add(1))}})
		require.NoError(t, err)
		evt.Backend = name
		require.NoError(t, p.backends[name].buffer.Enqueue(testContext(t), evt, token))
	}
	_, err := p.backends[name].buffer.Flush(testContext(t))
	require.NoError(t, err)
}

func initialSubscription(t *testing.T, p *Puller, opts events.SubscribeOptions) (events.Subscription, string) {
	t.Helper()
	initializeTestBoundaries(t, p)
	sub, err := p.Subscribe(testContext(t), opts)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() })
	envelope, err := sub.Next(testContext(t))
	require.NoError(t, err)
	require.Nil(t, envelope.Change)
	require.NotEmpty(t, envelope.Progress)
	return sub, envelope.Progress
}

func nextEnvelope(t *testing.T, sub events.Subscription) *events.PullerEvent {
	t.Helper()
	event, err := sub.Next(testContext(t))
	require.NoError(t, err)
	return event
}

func marker(t *testing.T, token string) *cursor.ProgressMarker {
	t.Helper()
	m, err := cursor.DecodeProgressMarker(token)
	require.NoError(t, err)
	return m
}

func sequence(t *testing.T, token, source string) uint64 {
	t.Helper()
	position, ok := marker(t, token).GetPosition(source)
	require.True(t, ok)
	return position.Sequence
}

func initializeTestBoundaries(t *testing.T, p *Puller) {
	t.Helper()
	initial := primitive.Timestamp{T: 100, I: 1}
	for _, backend := range p.backends {
		require.NoError(t, backend.buffer.InitializeBoundary(testContext(t), nil, &initial))
	}
}
