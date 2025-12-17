package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"syntrix/internal/storage"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoBackend struct {
	client         *mongo.Client
	db             *mongo.Database
	dataCollection string
	sysCollection  string
}

func (m *MongoBackend) getCollection(nameOrPath string) *mongo.Collection {
	if nameOrPath == "sys" || strings.HasPrefix(nameOrPath, "sys/") {
		return m.db.Collection(m.sysCollection)
	}
	return m.db.Collection(m.dataCollection)
}

// NewMongoBackend initializes a new MongoDB storage backend
func NewMongoBackend(ctx context.Context, uri string, dbName string, dataColl string, sysColl string) (*MongoBackend, error) {
	clientOpts := options.Client().ApplyURI(uri)
	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return nil, err
	}

	// Ping the database to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}

	return &MongoBackend{
		client:         client,
		db:             client.Database(dbName),
		dataCollection: dataColl,
		sysCollection:  sysColl,
	}, nil
}

func (m *MongoBackend) Get(ctx context.Context, path string) (*storage.Document, error) {
	collection := m.getCollection(path)

	var doc storage.Document
	err := collection.FindOne(ctx, bson.M{"_id": path}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, storage.ErrNotFound
		}
		return nil, err
	}

	return &doc, nil
}

func (m *MongoBackend) Create(ctx context.Context, doc *storage.Document) error {
	collection := m.getCollection(doc.Collection)

	_, err := collection.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		return errors.New("document already exists")
	}
	return err
}

func (m *MongoBackend) Update(ctx context.Context, path string, data map[string]interface{}, version int64) error {
	collection := m.getCollection(path)

	filter := bson.M{"_id": path}
	if version > 0 {
		filter["version"] = version
	}

	update := bson.M{
		"$set": bson.M{
			"data":       data,
			"updated_at": time.Now().UnixNano(),
		},
		"$inc": bson.M{
			"version": 1,
		},
	}

	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		count, _ := collection.CountDocuments(ctx, bson.M{"_id": path})
		if count == 0 {
			return storage.ErrNotFound
		}
		return storage.ErrVersionConflict
	}

	return nil
}

func (m *MongoBackend) Delete(ctx context.Context, path string) error {
	collection := m.getCollection(path)
	_, err := collection.DeleteOne(ctx, bson.M{"_id": path})
	return err
}

func (m *MongoBackend) Query(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	collection := m.getCollection(q.Collection)

	filter := bson.M{"collection": q.Collection}

	for _, f := range q.Filters {
		fieldName := mapField(f.Field)
		op := mapOp(f.Op)
		if op == "" {
			continue // Or return error
		}
		filter[fieldName] = bson.M{op: f.Value}
	}

	findOptions := options.Find()
	if q.Limit > 0 {
		findOptions.SetLimit(int64(q.Limit))
	}

	if len(q.OrderBy) > 0 {
		sort := bson.D{}
		for _, o := range q.OrderBy {
			dir := 1
			if o.Direction == "desc" {
				dir = -1
			}
			sort = append(sort, bson.E{Key: mapField(o.Field), Value: dir})
		}
		findOptions.SetSort(sort)
	}

	// TODO: Implement StartAfter (Cursor)

	cursor, err := collection.Find(ctx, filter, findOptions)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []*storage.Document
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	return docs, nil
}

func (m *MongoBackend) Watch(ctx context.Context, collectionName string, resumeToken interface{}) (<-chan storage.Event, error) {
	pipeline := mongo.Pipeline{}
	if collectionName != "" {
		// Filter by documentKey._id starting with "collectionName/"
		// This works for delete events too, unlike filtering by fullDocument.collection
		pattern := fmt.Sprintf("^%s/", collectionName)
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{{Key: "documentKey._id", Value: bson.D{{Key: "$regex", Value: pattern}}}}}})
	}

	// We need 'updateLookup' to get the full document after an update
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	if resumeToken != nil {
		opts.SetResumeAfter(resumeToken)
	}

	stream, err := m.getCollection(collectionName).Watch(ctx, pipeline, opts)
	if err != nil {
		return nil, err
	}

	out := make(chan storage.Event)

	go func() {
		defer close(out)
		defer stream.Close(ctx)

		for stream.Next(ctx) {
			var changeEvent struct {
				ID            interface{}       `bson:"_id"`
				OperationType string            `bson:"operationType"`
				FullDocument  *storage.Document `bson:"fullDocument"`
				DocumentKey   struct {
					ID string `bson:"_id"`
				} `bson:"documentKey"`
				ClusterTime interface{} `bson:"clusterTime"` // Timestamp
			}

			if err := stream.Decode(&changeEvent); err != nil {
				// Log error?
				continue
			}

			evt := storage.Event{
				Path:        changeEvent.DocumentKey.ID,
				ResumeToken: changeEvent.ID,
				// Timestamp: ... (ClusterTime is complex, let's use current time or parse it if needed)
				Timestamp: time.Now().UnixNano(),
			}

			switch changeEvent.OperationType {
			case "insert":
				evt.Type = storage.EventCreate
				evt.Document = changeEvent.FullDocument
			case "update", "replace":
				evt.Type = storage.EventUpdate
				evt.Document = changeEvent.FullDocument
			case "delete":
				evt.Type = storage.EventDelete
			default:
				continue
			}

			select {
			case out <- evt:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func mapField(field string) string {
	switch field {
	case "path", "_id":
		return "_id"
	case "collection":
		return "collection"
	case "updated_at":
		return "updated_at"
	case "version":
		return "version"
	default:
		return "data." + field
	}
}

func mapOp(op string) string {
	switch op {
	case "==":
		return "$eq"
	case "!=":
		return "$ne"
	case ">":
		return "$gt"
	case ">=":
		return "$gte"
	case "<":
		return "$lt"
	case "<=":
		return "$lte"
	case "in":
		return "$in"
	default:
		return "$eq" // Default to equality
	}
}

func (m *MongoBackend) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}
