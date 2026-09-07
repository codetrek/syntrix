package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syntrixbase/syntrix/internal/core/storage"
	"github.com/syntrixbase/syntrix/internal/puller/config"
	"github.com/syntrixbase/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func realMongoFixture(t *testing.T) (*mongo.Client, *mongo.Database) {
	t.Helper()
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	client, err := mongo.Connect(testContext(t), options.Client().ApplyURI(uri))
	require.NoError(t, err)
	db := client.Database("test_puller_source_" + primitive.NewObjectID().Hex())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, db.Drop(ctx))
		assert.NoError(t, client.Disconnect(ctx))
	})
	require.NoError(t, client.Ping(testContext(t), nil))
	return client, db
}

func realMongoPuller(t *testing.T, cfg config.Config, client *mongo.Client, db *mongo.Database, collections []string) *Puller {
	t.Helper()
	p := New(cfg, nil)
	require.NoError(t, p.AddBackend("primary", client, db.Name(), config.PullerBackendConfig{
		Name: "primary", SourceID: "real-source", Collections: collections,
	}))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, p.Stop(ctx))
	})
	return p
}

func TestPullerRealMongoSourceAndResume(t *testing.T) {
	for _, tc := range []struct {
		name, mode  string
		collections []string
	}{
		{"current_filtered", "from_now", []string{"documents"}},
		{"retained_filtered", "from_beginning", []string{"documents"}},
		{"current_all_collections", "from_now", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, db := realMongoFixture(t)
			for _, collection := range []string{"documents", "other"} {
				require.NoError(t, db.CreateCollection(testContext(t), collection))
			}
			insert := func(collection, id string) {
				t.Helper()
				doc := storage.StoredDoc{Id: id, Database: "logical-db", Collection: "items",
					Fullpath: "items/" + id, Version: 1, Data: map[string]any{"value": id}}
				_, err := db.Collection(collection).InsertOne(testContext(t), doc)
				require.NoError(t, err)
			}
			var expected []string
			if tc.mode == "from_beginning" {
				insert("documents", "historical")
				expected = append(expected, "historical")
			}
			cfg := newTestConfig(t)
			cfg.Bootstrap.Mode = tc.mode
			p := realMongoPuller(t, cfg, client, db, tc.collections)
			beginning, err := p.heads.Encode()
			require.NoError(t, err)
			lifetime, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			t.Cleanup(cancel)
			require.NoError(t, p.Start(lifetime))
			state, err := p.backends["primary"].buffer.State()
			require.NoError(t, err)
			if len(state.ResumeToken) == 0 {
				require.NotNil(t, state.StartAt, "readiness requires a durable initial source boundary")
				require.NotZero(t, state.StartAt.T)
			}
			if tc.mode == "from_now" {
				require.Zero(t, state.Position.Sequence)
			}
			sub, err := p.Subscribe(testContext(t), events.SubscribeOptions{After: beginning})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, sub.Close()) })
			initial := nextEnvelope(t, sub)
			require.Nil(t, initial.Change)
			require.Zero(t, sequence(t, initial.Progress, "real-source"))
			insert("other", "outside-one")
			insert("documents", "one")
			insert("other", "outside-two")
			insert("documents", "two")
			if tc.collections == nil {
				expected = append(expected, "outside-one", "one", "outside-two", "two")
			} else {
				expected = append(expected, "one", "two")
			}
			var checkpoint string
			for index, id := range expected {
				envelope := nextEnvelope(t, sub)
				require.NotNil(t, envelope.Change)
				assert.Equal(t, id, envelope.Change.MgoDocID)
				assert.Equal(t, "logical-db", envelope.Change.Database)
				assert.Equal(t, events.StoreOperationInsert, envelope.Change.OpType)
				require.NotNil(t, envelope.Change.FullDocument)
				assert.Equal(t, id, envelope.Change.FullDocument.Data["value"])
				if tc.collections != nil {
					assert.Equal(t, "documents", envelope.Change.MgoColl)
				}
				assert.Equal(t, uint64(index+1), sequence(t, envelope.Progress, "real-source"))
				checkpoint = envelope.Progress
			}
			require.NoError(t, sub.Close())
			require.NoError(t, p.Stop(testContext(t)))
			insert("documents", "during-downtime")
			resumed := realMongoPuller(t, cfg, client, db, tc.collections)
			require.NoError(t, resumed.Start(lifetime))
			replay, err := resumed.Subscribe(testContext(t), events.SubscribeOptions{After: checkpoint})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, replay.Close()) })
			require.Equal(t, checkpoint, nextEnvelope(t, replay).Progress)
			event := nextEnvelope(t, replay)
			assert.Equal(t, "during-downtime", event.Change.MgoDocID)
			assert.Equal(t, uint64(len(expected)+1), sequence(t, event.Progress, "real-source"))
			require.NoError(t, resumed.Err())
		})
	}
}

func TestMongoBootstrapHelpersPropagateFailures(t *testing.T) {
	client, db := realMongoFixture(t)
	oldest, err := oldestOplogTimestamp(testContext(t), client)
	require.NoError(t, err)
	current, err := currentOperationTime(testContext(t), client)
	require.NoError(t, err)
	require.NotZero(t, oldest.T)
	require.NotZero(t, current.T)
	assert.LessOrEqual(t, events.ClusterTimeFromPrimitive(oldest).Compare(events.ClusterTimeFromPrimitive(current)), 0)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = oldestOplogTimestamp(canceled, client)
	require.ErrorIs(t, err, context.Canceled)
	_, err = currentOperationTime(canceled, client)
	require.ErrorIs(t, err, context.Canceled)
	_, err = openMongoChangeStream(canceled, db, nil, options.ChangeStream())
	require.ErrorIs(t, err, context.Canceled)
	disconnected, err := mongo.NewClient(options.Client())
	require.NoError(t, err)
	_, err = currentOperationTime(testContext(t), disconnected)
	require.ErrorIs(t, err, mongo.ErrClientDisconnected)
	_, err = oldestOplogTimestamp(testContext(t), disconnected)
	require.ErrorIs(t, err, mongo.ErrClientDisconnected)
}

func TestMongoBootstrapRejectsMissingMetadata(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing_operation_time", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateSuccessResponse())
		_, err := currentOperationTime(testContext(mt.T), mt.Client)
		require.ErrorContains(mt, err, "source returned no initial operation time")
	})
	mt.Run("missing_oplog_timestamp", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCursorResponse(0, "local.oplog.rs", mtest.FirstBatch, bson.D{{Key: "op", Value: "n"}}))
		_, err := oldestOplogTimestamp(testContext(mt.T), mt.Client)
		require.ErrorContains(mt, err, "earliest oplog entry has no timestamp")
	})
}
