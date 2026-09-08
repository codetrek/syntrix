package checkpoint

import (
	"context"
	"encoding/hex"
	"slices"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// ReadMongoSource requires every allowlisted collection to exist physically and
// expose its native UUID. Metadata failures preserve their cause; a missing
// collection is a source mismatch and must not trigger implicit creation.
func ReadMongoSource(ctx context.Context, db *mongo.Database, sourceID string, collectionNames []string) (MongoSource, error) {
	if db == nil {
		return MongoSource{}, &Error{Code: SourceUnavailable, Message: "MongoDB database is unavailable"}
	}
	if !validSourceName(sourceID) || !validSourceName(db.Name()) || len(collectionNames) == 0 || len(collectionNames) > maxCollections {
		return MongoSource{}, &Error{Code: InvalidCheckpoint, Message: "MongoDB source identity or collection allowlist is invalid"}
	}
	names := slices.Clone(collectionNames)
	slices.Sort(names)
	for i, name := range names {
		if !validSourceName(name) || (i > 0 && names[i-1] == name) {
			return MongoSource{}, &Error{Code: InvalidCheckpoint, Message: "MongoDB collection allowlist contains invalid or duplicate names"}
		}
	}
	filter := bson.D{{Key: "name", Value: bson.D{{Key: "$in", Value: names}}}}
	specifications, err := db.ListCollectionSpecifications(ctx, filter)
	if err != nil {
		return MongoSource{}, &Error{Code: SourceUnavailable, Message: "cannot read MongoDB source metadata", Cause: err}
	}
	byName := make(map[string]*mongo.CollectionSpecification, len(specifications))
	for _, specification := range specifications {
		if specification == nil || !slices.Contains(names, specification.Name) {
			return MongoSource{}, &Error{Code: SourceUnavailable, Message: "MongoDB returned unexpected collection metadata"}
		}
		if _, exists := byName[specification.Name]; exists {
			return MongoSource{}, &Error{Code: SourceUnavailable, Message: "MongoDB returned duplicate collection metadata"}
		}
		byName[specification.Name] = specification
	}
	source := MongoSource{ID: sourceID, Database: db.Name(), Collections: make([]MongoCollection, len(names))}
	for i, name := range names {
		specification, exists := byName[name]
		if !exists {
			return MongoSource{}, &Error{Code: SourceMismatch, Message: "allowlisted MongoDB collection does not exist"}
		}
		if specification.Type != "collection" || specification.UUID == nil || specification.UUID.Subtype != 4 || len(specification.UUID.Data) != 16 {
			return MongoSource{}, &Error{Code: SourceUnavailable, Message: "MongoDB source requires physical collections with native UUIDs"}
		}
		source.Collections[i] = MongoCollection{Name: name, UUID: hex.EncodeToString(specification.UUID.Data)}
	}
	return source, nil
}
