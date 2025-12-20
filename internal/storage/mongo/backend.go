package mongo

import (
	"context"
	"errors"
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

func (m *MongoBackend) DB() *mongo.Database {
	return m.db
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

func (m *MongoBackend) Get(ctx context.Context, fullpath string) (*storage.Document, error) {
	collection := m.getCollection(fullpath)
	id := storage.CalculateID(fullpath)

	var doc storage.Document
	err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&doc)
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
		return storage.ErrExists
	}
	return err
}

func (m *MongoBackend) Update(ctx context.Context, path string, data map[string]interface{}, precond storage.Filters) error {
	collection := m.getCollection(path)
	id := storage.CalculateID(path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id

	update := bson.M{
		"$set": bson.M{
			"data":       data,
			"updated_at": time.Now().UnixMilli(),
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
		count, _ := collection.CountDocuments(ctx, bson.M{"_id": id})
		if count == 0 {
			return storage.ErrNotFound
		}
		return storage.ErrVersionConflict
	}

	return nil
}

func (m *MongoBackend) Patch(ctx context.Context, path string, data map[string]interface{}, precond storage.Filters) error {
	collection := m.getCollection(path)
	id := storage.CalculateID(path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id

	updates := bson.M{
		"updated_at": time.Now().UnixMilli(),
	}
	for k, v := range data {
		updates["data."+k] = v
	}

	update := bson.M{
		"$set": updates,
		"$inc": bson.M{
			"version": 1,
		},
	}

	result, err := collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}

	if result.MatchedCount == 0 {
		count, _ := collection.CountDocuments(ctx, bson.M{"_id": id})
		if count == 0 {
			return storage.ErrNotFound
		}
		return storage.ErrVersionConflict
	}

	return nil
}

func (m *MongoBackend) Delete(ctx context.Context, path string) error {
	collection := m.getCollection(path)
	id := storage.CalculateID(path)
	_, err := collection.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (m *MongoBackend) Query(ctx context.Context, q storage.Query) ([]*storage.Document, error) {
	collection := m.getCollection(q.Collection)

	filter := makeFilterBSON(q.Filters)
	filter["collection"] = q.Collection

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

func (m *MongoBackend) Watch(ctx context.Context, collectionName string, resumeToken interface{}, opts storage.WatchOptions) (<-chan storage.Event, error) {
	pipeline := mongo.Pipeline{}
	if collectionName != "" {
		// Filter by fullDocument.collection for insert/update/replace
		// OR operationType is delete (since we can't filter deletes by collection with hash ID)
		match := bson.D{
			{Key: "$or", Value: bson.A{
				bson.D{{Key: "fullDocument.collection", Value: collectionName}},
				bson.D{{Key: "operationType", Value: "delete"}},
			}},
		}
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: match}})
	}

	// We need 'updateLookup' to get the full document after an update
	changeStreamOpts := options.ChangeStream().SetFullDocument(options.UpdateLookup)
	if opts.IncludeBefore {
		changeStreamOpts.SetFullDocumentBeforeChange("whenAvailable")
	}
	if resumeToken != nil {
		changeStreamOpts.SetResumeAfter(resumeToken)
	}

	stream, err := m.getCollection(collectionName).Watch(ctx, pipeline, changeStreamOpts)
	if err != nil {
		return nil, err
	}

	out := make(chan storage.Event)

	go func() {
		defer close(out)
		defer stream.Close(ctx)

		for stream.Next(ctx) {
			var changeEvent struct {
				ID                       interface{}       `bson:"_id"`
				OperationType            string            `bson:"operationType"`
				FullDocument             *storage.Document `bson:"fullDocument"`
				FullDocumentBeforeChange *storage.Document `bson:"fullDocumentBeforeChange"`
				DocumentKey              struct {
					ID string `bson:"_id"`
				} `bson:"documentKey"`
				ClusterTime interface{} `bson:"clusterTime"` // Timestamp
			}

			if err := stream.Decode(&changeEvent); err != nil {
				continue
			}

			// Client-side filtering for collection (double check)
			if collectionName != "" && changeEvent.OperationType != "delete" {
				if changeEvent.FullDocument == nil || changeEvent.FullDocument.Collection != collectionName {
					continue
				}
			}

			evt := storage.Event{
				Path:        changeEvent.DocumentKey.ID,
				ResumeToken: changeEvent.ID,
				// Timestamp: ... (ClusterTime is complex, let's use current time or parse it if needed)
				Timestamp: time.Now().UnixNano(),
				Before:    changeEvent.FullDocumentBeforeChange,
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

func (m *MongoBackend) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}

func (m *MongoBackend) Transaction(ctx context.Context, fn func(ctx context.Context, tx storage.StorageBackend) error) error {
	session, err := m.client.StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)

	callback := func(sessCtx mongo.SessionContext) (interface{}, error) {
		return nil, fn(sessCtx, m)
	}

	_, err = session.WithTransaction(ctx, callback)
	return err
}
