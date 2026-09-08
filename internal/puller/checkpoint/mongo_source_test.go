package checkpoint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func collectionMetadata(name, kind string, uuid any) bson.D {
	return bson.D{{Key: "name", Value: name}, {Key: "type", Value: kind}, {Key: "info", Value: bson.D{{Key: "uuid", Value: uuid}}}}
}

func TestReadMongoSource(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("canonical scope and native identity", func(mt *mtest.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		uuidA := primitive.Binary{Subtype: 4, Data: []byte{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}}
		uuidZ := primitive.Binary{Subtype: 4, Data: []byte{171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171, 171}}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".$cmd.listCollections", mtest.FirstBatch,
			collectionMetadata("zeta", "collection", uuidZ), collectionMetadata("alpha", "collection", uuidA)))
		names := []string{"zeta", "alpha"}
		source, err := ReadMongoSource(ctx, mt.DB, "storage-a", names)
		require.NoError(t, err)
		expected := testSource()
		expected.Database = mt.DB.Name()
		expected.Collections[0], expected.Collections[1] = expected.Collections[1], expected.Collections[0]
		require.Equal(t, expected, source)
		require.Equal(t, []string{"zeta", "alpha"}, names)
		commands := mt.GetAllStartedEvents()
		require.Len(t, commands, 1)
		require.Equal(t, "listCollections", commands[0].CommandName)
		filter := commands[0].Command.Lookup("filter").Document().Lookup("name").Document().Lookup("$in").Array()
		values, err := filter.Values()
		require.NoError(t, err)
		require.Len(t, values, 2)
		require.Equal(t, "alpha", values[0].StringValue())
		require.Equal(t, "zeta", values[1].StringValue())
	})

	uuid := primitive.Binary{Subtype: 4, Data: make([]byte, 16)}
	for _, test := range []struct {
		name      string
		documents []bson.D
		code      ErrorCode
	}{
		{"missing collection", nil, SourceMismatch},
		{"view", []bson.D{collectionMetadata("docs", "view", uuid)}, SourceUnavailable},
		{"missing UUID", []bson.D{{{Key: "name", Value: "docs"}, {Key: "type", Value: "collection"}}}, SourceUnavailable},
		{"wrong UUID subtype", []bson.D{collectionMetadata("docs", "collection", primitive.Binary{Subtype: 0, Data: make([]byte, 16)})}, SourceUnavailable},
		{"wrong UUID size", []bson.D{collectionMetadata("docs", "collection", primitive.Binary{Subtype: 4, Data: make([]byte, 15)})}, SourceUnavailable},
		{"unrequested collection", []bson.D{collectionMetadata("extra", "collection", uuid)}, SourceUnavailable},
		{"duplicate metadata", []bson.D{collectionMetadata("docs", "collection", uuid), collectionMetadata("docs", "collection", uuid)}, SourceUnavailable},
		{"invalid metadata BSON type", []bson.D{collectionMetadata("docs", "collection", "not-binary")}, SourceUnavailable},
	} {
		mt.Run(test.name, func(mt *mtest.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".$cmd.listCollections", mtest.FirstBatch, test.documents...))
			source, err := ReadMongoSource(ctx, mt.DB, "storage-a", []string{"docs"})
			requireCode(t, err, test.code)
			require.Empty(t, source)
			for _, command := range mt.GetAllStartedEvents() {
				require.Equal(t, "listCollections", command.CommandName, "source validation must not create collections")
			}
		})
	}
	mt.Run("metadata permission failure", func(mt *mtest.T) {
		mt.AddMockResponses(mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 13, Message: "private-native-token"}))
		_, err := ReadMongoSource(context.Background(), mt.DB, "storage-a", []string{"docs"})
		requireCode(t, err, SourceUnavailable)
		var native mongo.CommandError
		require.ErrorAs(t, err, &native)
		require.EqualValues(t, 13, native.Code)
		require.NotContains(t, err.Error(), "private-native-token")
	})
	mt.Run("canceled metadata read", func(mt *mtest.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := ReadMongoSource(ctx, mt.DB, "storage-a", []string{"docs"})
		requireCode(t, err, SourceUnavailable)
		require.ErrorIs(t, err, context.Canceled)
	})
	mt.Run("invalid identity or scope does not issue command", func(mt *mtest.T) {
		for _, test := range []struct {
			id    string
			names []string
		}{
			{"", []string{"docs"}},
			{"source", nil},
			{"source", []string{""}},
			{"source", []string{"docs", "docs"}},
			{"source", []string{strings.Repeat("a", maxSourceNameSize+1)}},
			{"source", make([]string, maxCollections+1)},
		} {
			_, err := ReadMongoSource(context.Background(), mt.DB, test.id, test.names)
			requireCode(t, err, InvalidCheckpoint)
		}
		require.Empty(t, mt.GetAllStartedEvents())
	})
	_, err := ReadMongoSource(context.Background(), nil, "source", []string{"docs"})
	requireCode(t, err, SourceUnavailable)
}
