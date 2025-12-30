package mongo

import (
	"github.com/codetrek/syntrix/pkg/model"
	"go.mongodb.org/mongo-driver/bson"
)

func makeFilterBSON(filters model.Filters) bson.M {
	bsonFilter := bson.M{}

	for _, f := range filters {
		fieldName := mapField(f.Field)
		op := mapOp(f.Op)
		if op == "" {
			continue // Or return error
		}
		bsonFilter[fieldName] = bson.M{op: f.Value}
	}

	return bsonFilter
}

func mapField(field string) string {
	switch field {
	case "_id":
		return "_id"
	case "collection":
		return "collection"
	case "collectionHash":
		return "collection_hash"
	case "updatedAt":
		return "updated_at"
	case "createdAt":
		return "created_at"
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
		return ""
	}
}
