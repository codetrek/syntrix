package checkpoint

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// MongoCaptureContract identifies database-level native change streams with
// fullDocument=updateLookup. The pipeline includes ns.coll in the allowlist,
// dropDatabase/invalidate events, and renames into an allowlisted to.coll.
// Lifecycle events invalidate the source before checkpoint admission. Changing
// these semantics requires a new contract; native tokens cannot bridge it.
const MongoCaptureContract = "database-change-stream/updateLookup/fixed-scope-with-lifecycle/v1"

const (
	maxCheckpointSize = 64 * 1024
	maxTokenSize      = 32 * 1024
	maxSourceNameSize = 1024
	maxCollections    = 1024
)

type MongoCollection struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// MongoSource uses a stable configured ID and native collection UUIDs. Names
// define the capture scope; UUIDs distinguish recreated or replaced collections.
type MongoSource struct {
	ID          string            `json:"id"`
	Database    string            `json:"database"`
	Collections []MongoCollection `json:"collections"`
}

// MongoPosition contains exactly one native position. ResumeAfter retains the
// original BSON bytes, without interpreting token fields or their ordering.
type MongoPosition struct {
	ResumeAfter bson.Raw
	StartAt     *primitive.Timestamp
}

type mongoTimestamp struct {
	Seconds   uint32 `json:"seconds"`
	Increment uint32 `json:"increment"`
}

type mongoEnvelope struct {
	Version     int             `json:"version"`
	Kind        string          `json:"kind"`
	Capture     string          `json:"capture"`
	Source      MongoSource     `json:"source"`
	ResumeAfter bson.Raw        `json:"resume_after,omitempty"`
	StartAt     *mongoTimestamp `json:"start_at,omitempty"`
}

func EncodeMongo(source MongoSource, token bson.Raw) (Checkpoint, error) {
	if err := validateMongoToken(token); err != nil {
		return "", err
	}
	return encodeMongo(source, token, nil)
}

// EncodeMongoStart binds an explicit bootstrap position. The caller owns the
// choice of timestamp and must not substitute it for failed resume history.
func EncodeMongoStart(source MongoSource, start primitive.Timestamp) (Checkpoint, error) {
	if start.T == 0 {
		return "", &Error{Code: InvalidCheckpoint, Message: "MongoDB start timestamp seconds must be nonzero"}
	}
	return encodeMongo(source, nil, &mongoTimestamp{Seconds: start.T, Increment: start.I})
}

func encodeMongo(source MongoSource, token bson.Raw, start *mongoTimestamp) (Checkpoint, error) {
	canonical, err := canonicalMongoSource(source)
	if err != nil {
		return "", err
	}
	envelope := mongoEnvelope{Version: 1, Kind: "mongo", Capture: MongoCaptureContract, Source: canonical, ResumeAfter: token, StartAt: start}
	data, err := json.Marshal(envelope)
	if err != nil {
		return "", &Error{Code: InvalidCheckpoint, Message: "cannot encode MongoDB checkpoint", Cause: err}
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	if len(encoded) > maxCheckpointSize {
		return "", &Error{Code: InvalidCheckpoint, Message: "checkpoint exceeds size limit"}
	}
	return Checkpoint(encoded), nil
}

func DecodeMongo(cp Checkpoint) (MongoSource, MongoPosition, error) {
	envelope, err := decodeMongoEnvelope(cp)
	if err != nil {
		return MongoSource{}, MongoPosition{}, err
	}
	position := MongoPosition{ResumeAfter: envelope.ResumeAfter}
	if envelope.StartAt != nil {
		position.StartAt = &primitive.Timestamp{T: envelope.StartAt.Seconds, I: envelope.StartAt.Increment}
	}
	return envelope.Source, position, nil
}

func decodeMongoEnvelope(cp Checkpoint) (mongoEnvelope, error) {
	var envelope mongoEnvelope
	if len(cp) == 0 || len(cp) > maxCheckpointSize {
		return envelope, &Error{Code: InvalidCheckpoint, Message: "checkpoint size is invalid"}
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(string(cp))
	if err != nil {
		return envelope, &Error{Code: InvalidCheckpoint, Message: "checkpoint base64 is invalid", Cause: err}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		return envelope, &Error{Code: InvalidCheckpoint, Message: "checkpoint envelope is invalid", Cause: err}
	}
	canonical, err := json.Marshal(envelope)
	// Re-encoding rejects missing, duplicate, reordered, and null fields, trailing
	// input, and alternate numeric or string encodings before checking semantics.
	if err != nil || !bytes.Equal(canonical, data) || base64.RawURLEncoding.EncodeToString(data) != string(cp) {
		return envelope, &Error{Code: InvalidCheckpoint, Message: "checkpoint encoding is not canonical", Cause: err}
	}
	if envelope.Version != 1 || envelope.Kind != "mongo" || envelope.Capture != MongoCaptureContract {
		return envelope, &Error{Code: IncompatibleState, Message: "checkpoint version or capture contract is unsupported"}
	}
	source, err := canonicalMongoSource(envelope.Source)
	if err != nil {
		return envelope, err
	}
	if !slices.Equal(source.Collections, envelope.Source.Collections) {
		return envelope, &Error{Code: InvalidCheckpoint, Message: "checkpoint collections are not in canonical order"}
	}
	if (envelope.ResumeAfter != nil) == (envelope.StartAt != nil) {
		return envelope, &Error{Code: InvalidCheckpoint, Message: "checkpoint requires exactly one native position"}
	}
	if envelope.StartAt != nil {
		if envelope.StartAt.Seconds == 0 {
			return envelope, &Error{Code: InvalidCheckpoint, Message: "MongoDB start timestamp seconds must be nonzero"}
		}
	} else if err := validateMongoToken(envelope.ResumeAfter); err != nil {
		return envelope, err
	}
	return envelope, nil
}

// MatchMongoSource accepts any replica reading the same source incarnation and
// scope. A reordered allowlist is equivalent; renamed scope is not.
func MatchMongoSource(cp Checkpoint, expected MongoSource) (MongoPosition, error) {
	source, position, err := DecodeMongo(cp)
	if err != nil {
		return MongoPosition{}, err
	}
	expected, err = canonicalMongoSource(expected)
	if err != nil {
		return MongoPosition{}, err
	}
	if source.ID != expected.ID || source.Database != expected.Database {
		return MongoPosition{}, &Error{Code: SourceMismatch, Message: "checkpoint belongs to a different MongoDB source"}
	}
	if len(source.Collections) != len(expected.Collections) {
		return MongoPosition{}, &Error{Code: ScopeMismatch, Message: "checkpoint collection allowlist differs from capture scope"}
	}
	for i, collection := range source.Collections {
		if collection.Name != expected.Collections[i].Name {
			return MongoPosition{}, &Error{Code: ScopeMismatch, Message: "checkpoint collection allowlist differs from capture scope"}
		}
	}
	for i, collection := range source.Collections {
		if collection.UUID != expected.Collections[i].UUID {
			return MongoPosition{}, &Error{Code: SourceMismatch, Message: "checkpoint collection incarnation no longer matches MongoDB"}
		}
	}
	return position, nil
}

func canonicalMongoSource(source MongoSource) (MongoSource, error) {
	if !validSourceName(source.ID) || !validSourceName(source.Database) || len(source.Collections) == 0 || len(source.Collections) > maxCollections {
		return MongoSource{}, &Error{Code: InvalidCheckpoint, Message: "MongoDB source identity or collection allowlist is invalid"}
	}
	source.Collections = slices.Clone(source.Collections)
	slices.SortFunc(source.Collections, func(a, b MongoCollection) int { return strings.Compare(a.Name, b.Name) })
	for i, collection := range source.Collections {
		uuid, err := hex.DecodeString(collection.UUID)
		if !validSourceName(collection.Name) || err != nil || len(uuid) != 16 || hex.EncodeToString(uuid) != collection.UUID {
			return MongoSource{}, &Error{Code: InvalidCheckpoint, Message: "MongoDB collection name or UUID is invalid"}
		}
		if i > 0 && source.Collections[i-1].Name == collection.Name {
			return MongoSource{}, &Error{Code: InvalidCheckpoint, Message: "MongoDB collection allowlist contains duplicate names"}
		}
	}
	return source, nil
}

func validSourceName(value string) bool {
	return value != "" && len(value) <= maxSourceNameSize && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validateMongoToken(token bson.Raw) error {
	if len(token) < 5 || len(token) > maxTokenSize {
		return &Error{Code: InvalidCheckpoint, Message: "MongoDB resume token size is invalid"}
	}
	if len(token) == 5 {
		return &Error{Code: InvalidCheckpoint, Message: "MongoDB resume token is empty"}
	}
	return validateBSONDocument(token, false, 0)
}
