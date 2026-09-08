package mongo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/syntrixbase/syntrix/internal/core/storage/types"
	"github.com/syntrixbase/syntrix/pkg/model"

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
	openStream          func(context.Context, *mongo.Collection, mongo.Pipeline, *options.ChangeStreamOptions) (changeStream, error)
	readSource          func(context.Context, *mongo.Collection, bool) (watchSource, error)
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

func (m *documentStore) Get(ctx context.Context, database string, fullpath string) (*types.StoredDoc, error) {
	collection := m.getCollection(fullpath)
	id := types.CalculateDatabase(database, fullpath)

	var doc types.StoredDoc
	err := collection.FindOne(ctx, bson.M{"_id": id, "database": database, "deleted": bson.M{"$ne": true}}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}

	return &doc, nil
}

func (m *documentStore) GetMany(ctx context.Context, database string, paths []string) ([]*types.StoredDoc, error) {
	if len(paths) == 0 {
		return []*types.StoredDoc{}, nil
	}
	ids := make([]string, len(paths))
	for i, path := range paths {
		ids[i] = types.CalculateDatabase(database, path)
	}
	collection := m.getCollection(paths[0])
	filter := bson.M{
		"_id":      bson.M{"$in": ids},
		"database": database,
		"deleted":  bson.M{"$ne": true},
	}
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	docMap := make(map[string]*types.StoredDoc)
	for cursor.Next(ctx) {
		var doc types.StoredDoc
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		docMap[doc.Id] = &doc
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	result := make([]*types.StoredDoc, len(paths))
	for i, id := range ids {
		result[i] = docMap[id]
	}
	return result, nil
}

func (m *documentStore) Create(ctx context.Context, database string, doc types.StoredDoc) error {
	collection := m.getCollection(doc.Collection)

	// Ensure derived fields are populated
	if doc.CollectionHash == "" {
		doc.CollectionHash = types.CalculateCollectionHash(doc.Collection)
	}
	doc.Database = database

	// Ensure soft-delete fields are reset
	doc.Deleted = false

	_, err := collection.InsertOne(ctx, doc)
	if mongo.IsDuplicateKeyError(err) {
		// Check if the document exists but is soft-deleted
		id := types.CalculateDatabase(database, doc.Fullpath)
		var existingDoc types.StoredDoc
		if findErr := collection.FindOne(ctx, bson.M{"_id": id, "database": database}).Decode(&existingDoc); findErr == nil {
			if existingDoc.Deleted {
				// Overwrite the soft-deleted document
				_, replaceErr := collection.ReplaceOne(ctx, bson.M{"_id": id, "database": database}, doc)
				return replaceErr
			}
		}
		return model.ErrExists
	}
	return err
}

func (m *documentStore) Update(ctx context.Context, database string, path string, data map[string]interface{}, precond model.Filters) error {
	collection := m.getCollection(path)
	id := types.CalculateDatabase(database, path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id
	filter["database"] = database
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
		count, _ := collection.CountDocuments(ctx, bson.M{"_id": id, "database": database})
		if count == 0 {
			return model.ErrNotFound
		}
		return model.ErrPreconditionFailed
	}

	return nil
}

func (m *documentStore) Patch(ctx context.Context, database string, path string, data map[string]interface{}, precond model.Filters) error {
	collection := m.getCollection(path)
	id := types.CalculateDatabase(database, path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id
	filter["database"] = database
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
		count, _ := collection.CountDocuments(ctx, bson.M{"_id": id, "database": database})
		if count == 0 {
			return model.ErrNotFound
		}
		return model.ErrPreconditionFailed
	}

	return nil
}

func (m *documentStore) Delete(ctx context.Context, database string, path string, precond model.Filters) error {
	collection := m.getCollection(path)
	id := types.CalculateDatabase(database, path)

	filter := makeFilterBSON(precond)
	filter["_id"] = id
	filter["database"] = database
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
		count, _ := collection.CountDocuments(ctx, bson.M{"_id": id, "database": database})
		if count == 0 {
			return model.ErrNotFound
		}
		// If document exists but matched count is 0, it means version conflict or already deleted
		// We can check if it is already deleted
		var doc types.StoredDoc
		if err := collection.FindOne(ctx, bson.M{"_id": id, "database": database}).Decode(&doc); err == nil {
			if doc.Deleted {
				return model.ErrNotFound // Already deleted
			}
		}
		return model.ErrPreconditionFailed
	}

	return nil
}

func (m *documentStore) DeleteByDatabase(ctx context.Context, database string, limit int) (int, error) {
	// Delete from data collection
	dataCollection := m.db.Collection(m.dataCollection)
	dataDeleted, err := m.deleteByDatabaseFromCollection(ctx, dataCollection, database, limit)
	if err != nil {
		return dataDeleted, err
	}

	// If we hit the limit in data collection, return early
	if limit > 0 && dataDeleted >= limit {
		return dataDeleted, nil
	}

	// Calculate remaining limit for sys collection
	remainingLimit := 0
	if limit > 0 {
		remainingLimit = limit - dataDeleted
	}

	// Delete from sys collection
	sysCollection := m.db.Collection(m.sysCollection)
	sysDeleted, err := m.deleteByDatabaseFromCollection(ctx, sysCollection, database, remainingLimit)
	if err != nil {
		return dataDeleted + sysDeleted, err
	}

	return dataDeleted + sysDeleted, nil
}

func (m *documentStore) deleteByDatabaseFromCollection(ctx context.Context, collection *mongo.Collection, database string, limit int) (int, error) {
	filter := bson.M{"database": database}

	if limit > 0 {
		// When limiting, we need to find documents first, then delete by IDs
		findOpts := options.Find().SetLimit(int64(limit)).SetProjection(bson.M{"_id": 1})
		cursor, err := collection.Find(ctx, filter, findOpts)
		if err != nil {
			return 0, err
		}
		defer cursor.Close(ctx)

		var ids []string
		for cursor.Next(ctx) {
			var doc struct {
				ID string `bson:"_id"`
			}
			if err := cursor.Decode(&doc); err != nil {
				return 0, err
			}
			ids = append(ids, doc.ID)
		}

		if err := cursor.Err(); err != nil {
			return 0, err
		}

		if len(ids) == 0 {
			return 0, nil
		}

		// Hard delete the documents by IDs
		result, err := collection.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
		if err != nil {
			return 0, err
		}
		return int(result.DeletedCount), nil
	}

	// No limit - delete all documents for this database
	result, err := collection.DeleteMany(ctx, filter)
	if err != nil {
		return 0, err
	}
	return int(result.DeletedCount), nil
}

func (m *documentStore) Query(ctx context.Context, database string, q model.Query) ([]*types.StoredDoc, error) {
	collection := m.getCollection(q.Collection)

	filter := makeFilterBSON(q.Filters)
	filter["database"] = database
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

	var docs []*types.StoredDoc
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}

	for _, d := range docs {
		d.Collection = q.Collection // Ensure collection is set
	}

	return docs, nil
}

// EnsureIndexes creates necessary indexes
func (s *documentStore) EnsureIndexes(ctx context.Context) error {
	coll := s.getCollection("")

	// (database, collection_hash)
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "database", Value: 1}, {Key: "collection_hash", Value: 1}},
		Options: options.Index().SetUnique(false),
	})
	if err != nil {
		return err
	}

	// Revocation TTL index (wait, revocation is separate now. But soft delete uses sys_expires_at)
	// "sys_expires_at" is used for soft delete retention.
	_, err = coll.Indexes().CreateOne(ctx, mongo.IndexModel{
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
