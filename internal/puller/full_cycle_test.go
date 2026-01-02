package puller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pullerv1 "github.com/codetrek/syntrix/api/puller/v1"
	"github.com/codetrek/syntrix/internal/config"
	"github.com/codetrek/syntrix/internal/puller/internal/core"
	pullergrpc "github.com/codetrek/syntrix/internal/puller/internal/grpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupIntegrationEnv(t *testing.T) (*mongo.Collection, *core.Puller, *pullergrpc.Server, pullerv1.PullerServiceClient, func()) {
	// Setup MongoDB connection

	// 1. Setup MongoDB
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := strings.ReplaceAll(t.Name(), "/", "_") + "_" + fmt.Sprintf("%d", time.Now().UnixNano())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	require.NoError(t, err)

	// Create collection
	collName := "test_collection"
	err = client.Database(dbName).CreateCollection(ctx, collName)
	require.NoError(t, err)
	coll := client.Database(dbName).Collection(collName)

	// 2. Configure Puller
	tmpDir := t.TempDir()
	cfg := config.PullerConfig{
		Buffer: config.BufferConfig{
			Path:          filepath.Join(tmpDir, "buffer"),
			BatchSize:     10,
			BatchInterval: 10 * time.Millisecond,
			QueueSize:     100,
			MaxSize:       "10MB",
		},
		Cleaner: config.CleanerConfig{
			Retention: 1 * time.Hour,
			Interval:  1 * time.Minute,
		},
		Bootstrap: config.BootstrapConfig{
			Mode: "from_now",
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pullerCore := core.New(cfg, logger)
	pullerCore.SetRetryDelay(10 * time.Millisecond)
	pullerCore.SetBackpressureSlowDownDelay(10 * time.Millisecond)
	pullerCore.SetBackpressurePauseDelay(50 * time.Millisecond)

	// Add backend
	backendCfg := config.PullerBackendConfig{
		Name:        "backend1",
		Collections: []string{collName},
	}
	err = pullerCore.AddBackend("backend1", client, dbName, backendCfg)
	require.NoError(t, err)

	// Start Puller Core
	err = pullerCore.Start(context.Background())
	require.NoError(t, err)

	// 3. Start gRPC Server
	grpcPort := getFreePort(t)
	grpcCfg := config.PullerGRPCConfig{
		Address:        fmt.Sprintf(":%d", grpcPort),
		MaxConnections: 10,
	}
	grpcServer := pullergrpc.NewServer(grpcCfg, pullerCore, logger)
	err = grpcServer.Start(context.Background())
	require.NoError(t, err)

	// 4. Connect gRPC Client
	conn, err := grpc.NewClient(fmt.Sprintf("localhost:%d", grpcPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	grpcClient := pullerv1.NewPullerServiceClient(conn)

	cleanup := func() {
		conn.Close()
		grpcServer.Stop(context.Background())
		pullerCore.Stop(context.Background())
		_ = client.Database(dbName).Drop(context.Background())
		_ = client.Disconnect(context.Background())
	}

	return coll, pullerCore, grpcServer, grpcClient, cleanup
}

func TestPuller_FullCycle_DataIntegrity(t *testing.T) {
	coll, _, _, client, cleanup := setupIntegrationEnv(t)
	defer cleanup()

	ctx := context.Background()

	// Subscribe first
	stream, err := client.Subscribe(ctx, &pullerv1.SubscribeRequest{
		ConsumerId: "integrity-consumer",
	})
	require.NoError(t, err)

	// Insert 50 documents
	count := 50
	for i := 0; i < count; i++ {
		_, err := coll.InsertOne(ctx, bson.M{"_id": fmt.Sprintf("doc-%d", i), "val": i})
		require.NoError(t, err)
	}

	// Verify 50 events
	for i := 0; i < count; i++ {
		evt, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, "insert", evt.ChangeEvent.OpType)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), evt.ChangeEvent.MgoDocId)
	}
}

func TestPuller_FullCycle_Resilience(t *testing.T) {
	// Custom setup to allow restarting puller while keeping DB/Buffer
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		mongoURI = "mongodb://localhost:27017"
	}
	dbName := strings.ReplaceAll(t.Name(), "/", "_") + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
	collName := "test_collection"
	tmpDir := t.TempDir()
	bufferPath := filepath.Join(tmpDir, "buffer")

	ctx := context.Background()
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	require.NoError(t, err)
	defer func() {
		_ = mongoClient.Database(dbName).Drop(context.Background())
		_ = mongoClient.Disconnect(context.Background())
	}()

	err = mongoClient.Database(dbName).CreateCollection(ctx, collName)
	require.NoError(t, err)
	coll := mongoClient.Database(dbName).Collection(collName)

	// Helper to start puller
	startPuller := func() (*core.Puller, *pullergrpc.Server, pullerv1.PullerServiceClient, *grpc.ClientConn) {
		cfg := config.PullerConfig{
			Buffer: config.BufferConfig{
				Path:          bufferPath,
				BatchSize:     10,
				BatchInterval: 10 * time.Millisecond,
				QueueSize:     100,
				MaxSize:       "10MB",
			},
			Cleaner: config.CleanerConfig{
				Retention: 1 * time.Hour,
				Interval:  1 * time.Minute,
			},
			Bootstrap: config.BootstrapConfig{
				Mode: "from_now", // Should use checkpoint on restart
			},
		}
		logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
		p := core.New(cfg, logger)

		backendCfg := config.PullerBackendConfig{
			Name:        "backend1",
			Collections: []string{collName},
		}
		err := p.AddBackend("backend1", mongoClient, dbName, backendCfg)
		require.NoError(t, err)
		err = p.Start(ctx)
		require.NoError(t, err)

		grpcPort := getFreePort(t)
		grpcCfg := config.PullerGRPCConfig{
			Address:        fmt.Sprintf(":%d", grpcPort),
			MaxConnections: 10,
		}
		s := pullergrpc.NewServer(grpcCfg, p, logger)
		err = s.Start(ctx)
		require.NoError(t, err)

		conn, err := grpc.NewClient(fmt.Sprintf("localhost:%d", grpcPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		c := pullerv1.NewPullerServiceClient(conn)

		return p, s, c, conn
	}

	// 1. Start Puller
	p1, s1, c1, conn1 := startPuller()

	// 2. Subscribe and consume some events
	stream1, err := c1.Subscribe(ctx, &pullerv1.SubscribeRequest{ConsumerId: "resilience-1"})
	require.NoError(t, err)

	// Insert 10 docs
	for i := 0; i < 10; i++ {
		_, err := coll.InsertOne(ctx, bson.M{"_id": fmt.Sprintf("doc-%d", i)})
		require.NoError(t, err)
	}

	// Consume 5
	var lastToken string
	for i := 0; i < 5; i++ {
		evt, err := stream1.Recv()
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), evt.ChangeEvent.MgoDocId)
		lastToken = evt.Progress
	}

	// 3. Stop Puller (Simulate Crash)
	conn1.Close()
	s1.Stop(ctx)
	p1.Stop(ctx)

	// 4. Restart Puller
	p2, s2, c2, conn2 := startPuller()
	defer func() {
		conn2.Close()
		s2.Stop(ctx)
		p2.Stop(ctx)
	}()

	// 5. Subscribe with last token
	stream2, err := c2.Subscribe(ctx, &pullerv1.SubscribeRequest{
		ConsumerId: "resilience-2",
		After:      lastToken,
	})
	require.NoError(t, err)

	// 6. Consume remaining 5
	for i := 5; i < 10; i++ {
		evt, err := stream2.Recv()
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), evt.ChangeEvent.MgoDocId)
	}
}

func TestPuller_FullCycle_SlowConsumer(t *testing.T) {
	coll, _, _, client, cleanup := setupIntegrationEnv(t)
	defer cleanup()

	ctx := context.Background()

	// Subscribe
	stream, err := client.Subscribe(ctx, &pullerv1.SubscribeRequest{
		ConsumerId: "slow-consumer",
	})
	require.NoError(t, err)

	// Insert 20 docs fast
	for i := 0; i < 20; i++ {
		_, err := coll.InsertOne(ctx, bson.M{"_id": fmt.Sprintf("doc-%d", i)})
		require.NoError(t, err)
	}

	// Consume slowly
	for i := 0; i < 20; i++ {
		time.Sleep(10 * time.Millisecond) // Simulate processing time
		evt, err := stream.Recv()
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf("doc-%d", i), evt.ChangeEvent.MgoDocId)
	}
}
