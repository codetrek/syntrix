package puller

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pullerv1 "github.com/syntrixbase/syntrix/api/gen/puller/v1"
	pullerclient "github.com/syntrixbase/syntrix/internal/puller/client"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/cursor"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
)

const integrationSourceID = "puller-integration-mongo"

type pullerIntegrationEnv struct {
	t          *testing.T
	ctx        context.Context
	mongo      *mongo.Client
	collection *mongo.Collection
	cfg        config.Config
	local      LocalService
	remote     *pullerclient.Client
	server     *grpc.Server
	serveDone  chan error
	address    string
}

func newPullerIntegrationEnv(t *testing.T, modify func(*config.Config)) *pullerIntegrationEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	require.NoError(t, err)
	db := client.Database(fmt.Sprintf("puller_integration_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		assert.NoError(t, db.Drop(cleanupCtx))
		assert.NoError(t, client.Disconnect(cleanupCtx))
	})
	require.NoError(t, db.CreateCollection(ctx, "documents"))
	cfg := config.DefaultConfig()
	cfg.Backends = []config.PullerBackendConfig{{
		Name: "backend", SourceID: integrationSourceID, Collections: []string{"documents"},
	}}
	cfg.Buffer.Path = filepath.Join(t.TempDir(), "buffer")
	cfg.Buffer.BatchSize = 10
	cfg.Buffer.BatchInterval = 5 * time.Millisecond
	cfg.Buffer.QueueSize = 100
	cfg.Buffer.QueueBytes = 1 << 20
	cfg.Buffer.BatchBytes = 64 << 10
	cfg.Buffer.MaxSize = "10MB"
	cfg.GRPC.ChannelSize = 100
	cfg.GRPC.HeartbeatInterval = time.Hour
	cfg.Consumer.QueueBytes = 1 << 20
	cfg.Consumer.PageSize = 3
	cfg.Consumer.PageBytes = 64 << 10
	if modify != nil {
		modify(&cfg)
	}
	env := &pullerIntegrationEnv{t: t, ctx: ctx, mongo: client, collection: db.Collection("documents"), cfg: cfg}
	t.Cleanup(env.stop)
	env.start()
	return env
}

func (e *pullerIntegrationEnv) start() {
	e.t.Helper()
	logger := slog.Default()
	e.local = NewService(e.cfg, logger)
	require.NoError(e.t, e.local.AddBackend("backend", e.mongo, e.collection.Database().Name(), e.cfg.Backends[0]))
	require.NoError(e.t, e.local.Start(e.ctx))
	e.startTransport("127.0.0.1:0")
	var err error
	e.remote, err = pullerclient.New(e.address, logger)
	require.NoError(e.t, err)
}

func (e *pullerIntegrationEnv) startTransport(address string) {
	e.t.Helper()
	listener, err := net.Listen("tcp", address)
	require.NoError(e.t, err)
	e.address = listener.Addr().String()
	e.server = grpc.NewServer()
	pullerv1.RegisterPullerServiceServer(e.server, NewGRPCServer(e.cfg.GRPC, e.local, slog.Default()))
	e.serveDone = make(chan error, 1)
	server := e.server
	serveDone := e.serveDone
	go func() { serveDone <- server.Serve(listener) }()
}

func (e *pullerIntegrationEnv) stopTransport() {
	e.t.Helper()
	if e.server != nil {
		e.server.Stop()
		select {
		case err := <-e.serveDone:
			assert.NoError(e.t, err)
		case <-time.After(5 * time.Second):
			e.t.Error("gRPC server did not stop")
		}
		e.server = nil
	}
}

func (e *pullerIntegrationEnv) stop() {
	e.t.Helper()
	if e.remote != nil {
		assert.NoError(e.t, e.remote.Close())
		e.remote = nil
	}
	e.stopTransport()
	if e.local != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(e.t, e.local.Stop(ctx))
		e.local = nil
	}
}

func (e *pullerIntegrationEnv) insert(first, count int) {
	e.t.Helper()
	documents := make([]any, count)
	for i := range documents {
		n := first + i
		documents[i] = bson.M{
			"_id": fmt.Sprintf("doc-%d", n), "id": fmt.Sprintf("doc-%d", n),
			"database": "default", "collection": "items", "data": bson.M{"value": fmt.Sprintf("value-%d", n)},
		}
	}
	_, err := e.collection.InsertMany(e.ctx, documents)
	require.NoError(e.t, err)
}

func subscribeIntegration(t *testing.T, ctx context.Context, service Service, after string) events.Subscription {
	t.Helper()
	sub, err := service.Subscribe(ctx, events.SubscribeOptions{ConsumerID: t.Name(), After: after})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sub.Close()) })
	initial, err := sub.Next(ctx)
	require.NoError(t, err)
	require.NotNil(t, initial)
	require.Nil(t, initial.Change, "registration must deliver its effective starting position before document events")
	require.NotEmpty(t, initial.Progress)
	if after != "" {
		require.Equal(t, after, initial.Progress)
	}
	return sub
}

func nextIntegration(t *testing.T, ctx context.Context, sub events.Subscription) *events.PullerEvent {
	t.Helper()
	for {
		event, err := sub.Next(ctx)
		require.NoError(t, err)
		require.NotNil(t, event)
		if event.Change != nil {
			return event
		}
	}
}

func eventPosition(t *testing.T, event *events.PullerEvent) cursor.Position {
	t.Helper()
	marker, err := cursor.DecodeProgressMarker(event.Progress)
	require.NoError(t, err)
	position, found := marker.GetPosition(integrationSourceID)
	require.True(t, found)
	require.NotEmpty(t, position.Generation)
	return position
}

func TestPuller_FullCycle_DataIntegrity(t *testing.T) {
	env := newPullerIntegrationEnv(t, nil)
	local := subscribeIntegration(t, env.ctx, env.local, "")
	remote := subscribeIntegration(t, env.ctx, env.remote, "")
	env.insert(1, 50)
	var generation string
	for i := 1; i <= 50; i++ {
		localEvent := nextIntegration(t, env.ctx, local)
		remoteEvent := nextIntegration(t, env.ctx, remote)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), localEvent.Change.MgoDocID)
		assert.Equal(t, events.StoreOperationInsert, localEvent.Change.OpType)
		assert.Equal(t, localEvent.Change.EventID, remoteEvent.Change.EventID)
		assert.Equal(t, localEvent.Change.MgoDocID, remoteEvent.Change.MgoDocID)
		assert.Equal(t, localEvent.Change.Timestamp, remoteEvent.Change.Timestamp)
		assert.Equal(t, localEvent.Progress, remoteEvent.Progress)
		position := eventPosition(t, localEvent)
		assert.Equal(t, uint64(i), position.Sequence)
		if i == 1 {
			generation = position.Generation
		}
		assert.Equal(t, generation, position.Generation)
	}
}

func TestPuller_FullCycle_RestartPreservesReplayIdentity(t *testing.T) {
	env := newPullerIntegrationEnv(t, nil)
	observer := subscribeIntegration(t, env.ctx, env.local, "")
	env.insert(1, 12)
	original := make([]*events.PullerEvent, 12)
	for i := range original {
		original[i] = nextIntegration(t, env.ctx, observer)
	}
	after := original[4].Progress
	require.NoError(t, observer.Close())
	env.stop()

	// These writes must be recovered from Mongo's saved resume token; the other
	// seven records must be replayed with their original persistent identities.
	env.insert(13, 5)
	env.start()
	local := subscribeIntegration(t, env.ctx, env.local, after)
	remote := subscribeIntegration(t, env.ctx, env.remote, after)
	generation := eventPosition(t, original[0]).Generation
	for i := 6; i <= 17; i++ {
		localEvent := nextIntegration(t, env.ctx, local)
		remoteEvent := nextIntegration(t, env.ctx, remote)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), localEvent.Change.MgoDocID)
		assert.Equal(t, localEvent.Change.EventID, remoteEvent.Change.EventID)
		assert.Equal(t, localEvent.Progress, remoteEvent.Progress)
		position := eventPosition(t, localEvent)
		assert.Equal(t, uint64(i), position.Sequence)
		assert.Equal(t, generation, position.Generation)
		if i <= 12 {
			assert.Equal(t, original[i-1], localEvent)
		}
	}
}

func TestPuller_FullCycle_SlowConsumerCatchesUp(t *testing.T) {
	env := newPullerIntegrationEnv(t, func(cfg *config.Config) {
		cfg.GRPC.ChannelSize = 2
		cfg.Buffer.BatchSize = 2
	})
	lagging := subscribeIntegration(t, env.ctx, env.local, "")
	observer := subscribeIntegration(t, env.ctx, env.local, "")
	env.insert(1, 30)
	for i := 1; i <= 30; i++ {
		event := nextIntegration(t, env.ctx, observer)
		require.Equal(t, fmt.Sprintf("doc-%d", i), event.Change.MgoDocID)
	}
	// No document was read from lagging while all 30 records committed into a
	// two-record live queue. Recovery must cross pages without skipping records.
	for i := 1; i <= 30; i++ {
		event := nextIntegration(t, env.ctx, lagging)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), event.Change.MgoDocID)
		assert.Equal(t, uint64(i), eventPosition(t, event).Sequence)
	}
	env.insert(31, 1)
	assert.Equal(t, "doc-31", nextIntegration(t, env.ctx, lagging).Change.MgoDocID)
}
