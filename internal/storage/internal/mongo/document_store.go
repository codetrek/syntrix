package mongo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/codetrek/syntrix/internal/storage/types"
	"github.com/codetrek/syntrix/pkg/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type documentStore struct {
	client              *mongo.Client
	db                  *mongo.Database
	dataCollection      string
	sysCollection       string
	softDeleteRetention time.Duration
}

// NewDocumentStore initializes a new MongoDB document store
func NewDocumentStore(client *mongo.Client, db *mongo.Database, dataColl string, sysColl string, softDeleteRetention time.Duration) types.DocumentStore {
	return &documentStore{
		client:              client,
		db:                  db,
		dataCollection:      dataColl,
		sysCollection:       sysColl,
		softDeleteRetention: softDeleteRetention,
	}
}

func (m *documentStore) getCollection(nameOrPath string) *mongo.Collection {
	if nameOrPath == "sys" || strings.HasPrefix(nameOrPath, "sys/") {
		return m.db.Collection(m.sysCollection)
	}
	return m.db.Collection(m.dataCollection)
}

func (m *documentStore) Get(ctx context.Context, fullpath string) (*types.Document, error) {
	collection := m.getCollection(fullpath)
	id := types.CalculateID(fullpath)

	var doc types.Document
	err := collection.FindOne(ctx, bson.M{"_id": id, "deleted": bson.M{"$ne": true}}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}

	return &doc, nil
}

func (m *documentStore) Create(ctx context.Context, doc *types.Document) error {
	collection := m.getCollection(doc.Collection)

	// Ensure derived fields are populated
	if doc.CollectionHash == "" {
		doc.CollectionHash = types.CalculateCollectionHash(doc.Collection)
	}

	// Ensure soft-delete fields are reset
	doc.Deleted = false

	_, err := collection.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		// Check if the document exists but is soft-deleted
		id := types.CalculateID(doc.Fullpath)
		var existingDoc types.Document
		if findErr := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&existingDoc); findErr == nil {
			if existingDoc.Deleted {
				// Overwrite the soft-deleted document
				_, replaceErr := collection.ReplaceOne(ctx, bson.M{"_id": id}, doc)
				return replaceErr
			}
		}
		return model.ErrExists
	}
	return err
}

func (m *documentStore) Update(ctx context.Context, path string, data map[string]interface{}, precond model.Filters) error {
	collection := m.getCollection(path)
	id := types.CalculateID(path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id
	filter["deleted"] = bson.M{"$ne": true}

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
			return model.ErrNotFound
		}
		return model.ErrPreconditionFailed
	}

	return nil
}

func (m *documentStore) Patch(ctx context.Context, path string, data map[string]interface{}, precond model.Filters) error {
	collection := m.getCollection(path)
	id := types.CalculateID(path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id
	filter["deleted"] = bson.M{"$ne": true}

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
			return model.ErrNotFound
		}
		return model.ErrPreconditionFailed
	}

	return nil
}

func (m *documentStore) Delete(ctx context.Context, path string, precond model.Filters) error {
	collection := m.getCollection(path)
	id := types.CalculateID(path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id
	filter["deleted"] = bson.M{"$ne": true}

	update := bson.M{
		"$set": bson.M{
			"deleted":        true,
			"data":           bson.M{},
			"updated_at":     time.Now().UnixMilli(),
			"sys_expires_at": time.Now().Add(m.softDeleteRetention),
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
			return model.ErrNotFound
		}
		// If document exists but matched count is 0, it means version conflict or already deleted
		// We can check if it is already deleted
		var doc types.Document
		if err := collection.FindOne(ctx, bson.M{"_id": id}).Decode(&doc); err == nil {
			if doc.Deleted {
				return model.ErrNotFound // Already deleted
			}
		}
		return model.ErrPreconditionFailed
	}

	return nil
}

func (m *documentStore) Query(ctx context.Context, q model.Query) ([]*types.Document, error) {
	collection := m.getCollection(q.Collection)

	filter := makeFilterBSON(q.Filters)
	filter["collection_hash"] = types.CalculateCollectionHash(q.Collection)
	if !q.ShowDeleted {
		filter["deleted"] = bson.M{"$ne": true}
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

	var docs []*types.Document
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	return docs, nil
}

func (m *documentStore) Watch(ctx context.Context, collectionName string, resumeToken interface{}, opts types.WatchOptions) (<-chan types.Event, error) {
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

	out := make(chan types.Event)

	go func() {
		defer close(out)
		defer stream.Close(ctx)

		for stream.Next(ctx) {
			var changeEvent struct {
				ID                       interface{}     `bson:"_id"`
				OperationType            string          `bson:"operationType"`
				FullDocument             *types.Document `bson:"fullDocument"`
				FullDocumentBeforeChange *types.Document `bson:"fullDocumentBeforeChange"`
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

			evt := types.Event{
				Id:          changeEvent.DocumentKey.ID,
				ResumeToken: changeEvent.ID,
				// Timestamp: ... (ClusterTime is complex, let's use current time or parse it if needed)
				Timestamp: time.Now().UnixNano(),
				Before:    changeEvent.FullDocumentBeforeChange,
			}

			switch changeEvent.OperationType {
			case "insert":
				evt.Type = types.EventCreate
				evt.Document = changeEvent.FullDocument
			case "update", "replace":
				if changeEvent.FullDocument != nil && changeEvent.FullDocument.Deleted {
					evt.Type = types.EventDelete
				} else if changeEvent.OperationType == "replace" {
					// Replace operation on a non-deleted document is treated as a Create (re-creation)
					// This happens when overwriting a soft-deleted document
					evt.Type = types.EventCreate
					evt.Document = changeEvent.FullDocument
				} else if changeEvent.FullDocumentBeforeChange != nil && changeEvent.FullDocumentBeforeChange.Deleted {
					evt.Type = types.EventCreate
					evt.Document = changeEvent.FullDocument
				} else {
					evt.Type = types.EventUpdate
					evt.Document = changeEvent.FullDocument
				}
			case "delete":
				evt.Type = types.EventDelete
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

// EnsureIndexes creates necessary indexes
func (s *documentStore) EnsureIndexes(ctx context.Context) error {
	coll := s.getCollection("")

	indexes := []string{"collection_hash"}
	for _, field := range indexes {
		_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    bson.D{{Key: field, Value: 1}},
			Options: options.Index().SetUnique(false),
		})
		if err != nil {
			return err
		}
	}

	// Revocation TTL index (wait, revocation is separate now. But soft delete uses sys_expires_at)
	// "sys_expires_at" is used for soft delete retention.
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "sys_expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	return err
}

func (m *documentStore) Close(ctx context.Context) error {
	if m.client != nil {
		return m.client.Disconnect(ctx)
	}
	return nil
}
