package checkpoint

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func testSource() MongoSource {
	return MongoSource{ID: "storage-a", Database: "physical", Collections: []MongoCollection{
		{Name: "zeta", UUID: strings.Repeat("ab", 16)},
		{Name: "alpha", UUID: strings.Repeat("01", 16)},
	}}
}

func testToken(t *testing.T) bson.Raw {
	t.Helper()
	// Unknown fields, their BSON types, and document order remain native data.
	token, err := bson.Marshal(bson.D{
		{Key: "future", Value: bson.D{{Key: "z", Value: int32(3)}, {Key: "a", Value: int64(3)}}},
		{Key: "bytes", Value: primitive.Binary{Subtype: 0x80, Data: []byte{0, 1, 255}}},
		{Key: "opaque", Value: "not-a-lexicographic-position"},
	})
	require.NoError(t, err)
	return token
}

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var checkpointError *Error
	require.ErrorAs(t, err, &checkpointError)
	require.Equal(t, code, checkpointError.Code)
}

func encodeEnvelope(t *testing.T, envelope mongoEnvelope) Checkpoint {
	t.Helper()
	data, err := json.Marshal(envelope)
	require.NoError(t, err)
	return Checkpoint(base64.RawURLEncoding.EncodeToString(data))
}

func TestMongoCheckpointPortableAndOpaque(t *testing.T) {
	source := testSource()
	originalCollections := slices.Clone(source.Collections)
	token := testToken(t)
	cp, err := EncodeMongo(source, token)
	require.NoError(t, err)
	require.NoError(t, Validate(cp))
	require.Equal(t, originalCollections, source.Collections, "encoding must not reorder the caller's allowlist")

	slices.Reverse(source.Collections)
	otherReplicaCP, err := EncodeMongo(source, token)
	require.NoError(t, err)
	require.Equal(t, cp, otherReplicaCP)
	position, err := MatchMongoSource(cp, source)
	require.NoError(t, err)
	require.Equal(t, token, position.ResumeAfter)
	require.Nil(t, position.StartAt)

	decodedSource, decodedPosition, err := DecodeMongo(cp)
	require.NoError(t, err)
	require.Equal(t, "alpha", decodedSource.Collections[0].Name)
	require.Equal(t, source, decodedSource)
	require.Equal(t, token, decodedPosition.ResumeAfter)
	decodedPosition.ResumeAfter[4] = 0
	_, newPosition, err := DecodeMongo(cp)
	require.NoError(t, err)
	require.Equal(t, token, newPosition.ResumeAfter, "decoded positions must not share mutable storage")
}

func TestMongoStartCheckpoint(t *testing.T) {
	start := primitive.Timestamp{T: 100, I: 0}
	cp, err := EncodeMongoStart(testSource(), start)
	require.NoError(t, err)
	require.NoError(t, Validate(cp))
	_, position, err := DecodeMongo(cp)
	require.NoError(t, err)
	require.Nil(t, position.ResumeAfter)
	require.Equal(t, &start, position.StartAt)
	position, err = MatchMongoSource(cp, testSource())
	require.NoError(t, err)
	require.Equal(t, &start, position.StartAt)
	_, err = EncodeMongoStart(testSource(), primitive.Timestamp{})
	requireCode(t, err, InvalidCheckpoint)
	_, err = EncodeMongoStart(testSource(), primitive.Timestamp{I: 1})
	requireCode(t, err, InvalidCheckpoint)
}

func TestMongoCheckpointRejectsWrongBinding(t *testing.T) {
	cp, err := EncodeMongo(testSource(), testToken(t))
	require.NoError(t, err)
	tests := []struct {
		name   string
		change func(*MongoSource)
		code   ErrorCode
	}{
		{"source ID", func(s *MongoSource) { s.ID = "different-storage" }, SourceMismatch},
		{"database", func(s *MongoSource) { s.Database = "other-db" }, SourceMismatch},
		{"collection recreated", func(s *MongoSource) { s.Collections[0].UUID = strings.Repeat("ef", 16) }, SourceMismatch},
		{"collection added", func(s *MongoSource) {
			s.Collections = append(s.Collections, MongoCollection{Name: "new", UUID: strings.Repeat("ef", 16)})
		}, ScopeMismatch},
		{"collection renamed", func(s *MongoSource) { s.Collections[0].Name = "renamed" }, ScopeMismatch},
		{"scope before UUID", func(s *MongoSource) {
			s.Collections[1].UUID = strings.Repeat("ef", 16)
			s.Collections[0].Name = "renamed"
		}, ScopeMismatch},
		{"invalid expected source", func(s *MongoSource) { s.ID = "" }, InvalidCheckpoint},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := testSource()
			test.change(&source)
			position, err := MatchMongoSource(cp, source)
			requireCode(t, err, test.code)
			require.Empty(t, position)
		})
	}
	_, err = MatchMongoSource("invalid", testSource())
	requireCode(t, err, InvalidCheckpoint)
}

func TestMongoCheckpointRejectsMalformedEncoding(t *testing.T) {
	cp, err := EncodeMongo(testSource(), testToken(t))
	require.NoError(t, err)
	data, err := base64.RawURLEncoding.DecodeString(string(cp))
	require.NoError(t, err)
	envelopeJSON := string(data)
	jsonCP := func(value string) Checkpoint { return Checkpoint(base64.RawURLEncoding.EncodeToString([]byte(value))) }
	textCases := map[string]Checkpoint{
		"empty":                  "",
		"oversized":              Checkpoint(strings.Repeat("A", maxCheckpointSize+1)),
		"invalid base64":         "not base64!",
		"newline base64":         Checkpoint(string(cp[:20]) + "\n" + string(cp[20:])),
		"padded base64":          Checkpoint(string(cp) + "="),
		"invalid JSON":           jsonCP("{"),
		"unknown field":          jsonCP(strings.Replace(envelopeJSON, `"kind":"mongo"`, `"kind":"mongo","secret":"native-token"`, 1)),
		"duplicate field":        jsonCP(strings.Replace(envelopeJSON, `"version":1`, `"version":1,"version":1`, 1)),
		"duplicate nested field": jsonCP(strings.Replace(envelopeJSON, `"id":"storage-a"`, `"id":"storage-a","id":"storage-a"`, 1)),
		"missing version":        jsonCP(strings.Replace(envelopeJSON, `"version":1,`, "", 1)),
		"missing kind":           jsonCP(strings.Replace(envelopeJSON, `"kind":"mongo",`, "", 1)),
		"missing source ID":      jsonCP(strings.Replace(envelopeJSON, `"id":"storage-a",`, "", 1)),
		"trailing JSON":          jsonCP(envelopeJSON + "{}"),
		"trailing whitespace":    jsonCP(envelopeJSON + "\n"),
		"reordered fields":       jsonCP(strings.Replace(envelopeJSON, `"version":1,"kind":"mongo"`, `"kind":"mongo","version":1`, 1)),
		"null source":            jsonCP(strings.Replace(envelopeJSON, `"source":{`, `"source":null,"discard":{`, 1)),
		"fractional version":     jsonCP(strings.Replace(envelopeJSON, `"version":1`, `"version":1.0`, 1)),
		"null unused position":   jsonCP(strings.TrimSuffix(envelopeJSON, "}") + `,"start_at":null}`),
	}
	for name, malformed := range textCases {
		t.Run(name, func(t *testing.T) {
			source, position, err := DecodeMongo(malformed)
			requireCode(t, err, InvalidCheckpoint)
			require.Empty(t, source)
			require.Empty(t, position)
			assert.NotContains(t, err.Error(), "native-token")
		})
	}

	var envelope mongoEnvelope
	require.NoError(t, json.Unmarshal(data, &envelope))
	mutations := []struct {
		name   string
		change func(*mongoEnvelope)
		code   ErrorCode
	}{
		{"unsupported version", func(e *mongoEnvelope) { e.Version = 2 }, IncompatibleState},
		{"unsupported kind", func(e *mongoEnvelope) { e.Kind = "postgres" }, IncompatibleState},
		{"different options", func(e *mongoEnvelope) { e.Capture = "database-change-stream/whenAvailable/v1" }, IncompatibleState},
		{"missing lifecycle capture", func(e *mongoEnvelope) { e.Capture = "database-change-stream/updateLookup/ns.coll-allowlist/v1" }, IncompatibleState},
		{"empty identity", func(e *mongoEnvelope) { e.Source.ID = "" }, InvalidCheckpoint},
		{"empty database", func(e *mongoEnvelope) { e.Source.Database = "" }, InvalidCheckpoint},
		{"no scope", func(e *mongoEnvelope) { e.Source.Collections = nil }, InvalidCheckpoint},
		{"unordered scope", func(e *mongoEnvelope) { slices.Reverse(e.Source.Collections) }, InvalidCheckpoint},
		{"missing position", func(e *mongoEnvelope) { e.ResumeAfter = nil }, InvalidCheckpoint},
		{"both positions", func(e *mongoEnvelope) { e.StartAt = &mongoTimestamp{Seconds: 1} }, InvalidCheckpoint},
		{"zero start", func(e *mongoEnvelope) { e.ResumeAfter = nil; e.StartAt = &mongoTimestamp{} }, InvalidCheckpoint},
		{"malformed token", func(e *mongoEnvelope) { e.ResumeAfter = bson.Raw{1, 2, 3} }, InvalidCheckpoint},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			copy := envelope
			copy.Source.Collections = slices.Clone(copy.Source.Collections)
			mutation.change(&copy)
			requireCode(t, Validate(encodeEnvelope(t, copy)), mutation.code)
		})
	}
}

func TestMongoCheckpointRejectsInvalidSource(t *testing.T) {
	tests := map[string]func(*MongoSource){
		"empty ID":             func(s *MongoSource) { s.ID = "" },
		"oversized ID":         func(s *MongoSource) { s.ID = strings.Repeat("a", maxSourceNameSize+1) },
		"invalid UTF8":         func(s *MongoSource) { s.ID = string([]byte{0xff}) },
		"NUL database":         func(s *MongoSource) { s.Database = "db\x00name" },
		"empty collections":    func(s *MongoSource) { s.Collections = nil },
		"too many collections": func(s *MongoSource) { s.Collections = make([]MongoCollection, maxCollections+1) },
		"empty name":           func(s *MongoSource) { s.Collections[0].Name = "" },
		"duplicate name":       func(s *MongoSource) { s.Collections[0].Name = s.Collections[1].Name },
		"invalid hex":          func(s *MongoSource) { s.Collections[0].UUID = "g" + strings.Repeat("a", 31) },
		"short UUID":           func(s *MongoSource) { s.Collections[0].UUID = "01" },
		"uppercase UUID":       func(s *MongoSource) { s.Collections[0].UUID = strings.Repeat("AB", 16) },
		"dashed UUID":          func(s *MongoSource) { s.Collections[0].UUID = "123e4567-e89b-12d3-a456-426614174000" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			source := testSource()
			change(&source)
			cp, err := EncodeMongo(source, testToken(t))
			requireCode(t, err, InvalidCheckpoint)
			require.Empty(t, cp)
		})
	}
	source := testSource()
	source.Collections = make([]MongoCollection, 100)
	for i := range source.Collections {
		source.Collections[i] = MongoCollection{Name: strings.Repeat("x", 1000) + string(rune('a'+i)), UUID: strings.Repeat("01", 16)}
	}
	_, err := EncodeMongo(source, testToken(t))
	requireCode(t, err, InvalidCheckpoint)
	require.Contains(t, err.Error(), "size limit")
}

func TestMongoCheckpointRejectsMalformedNativeToken(t *testing.T) {
	valid := testToken(t)
	wrongLength := slices.Clone(valid)
	binary.LittleEndian.PutUint32(wrongLength, uint32(len(wrongLength)+1))
	invalidType := slices.Clone(valid)
	invalidType[4] = 0xfe
	missingTerminator := slices.Clone(valid)
	missingTerminator[len(missingTerminator)-1] = 1
	empty, err := bson.Marshal(bson.D{})
	require.NoError(t, err)
	duplicate, err := bson.Marshal(bson.D{{Key: "opaque", Value: "secret-token"}, {Key: "opaque", Value: "secret-token"}})
	require.NoError(t, err)
	for name, token := range map[string]bson.Raw{
		"nil":                nil,
		"short":              {1, 2},
		"oversized":          bytes.Repeat([]byte{1}, maxTokenSize+1),
		"wrong length":       wrongLength,
		"invalid type":       invalidType,
		"missing terminator": missingTerminator,
		"empty document":     empty,
		"duplicate fields":   duplicate,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := EncodeMongo(testSource(), token)
			requireCode(t, err, InvalidCheckpoint)
			require.NotContains(t, err.Error(), "secret-token")
		})
	}
}

func TestCheckpointErrorPreservesCauseWithoutRenderingIt(t *testing.T) {
	cause := errors.New("native-token-must-stay-private")
	err := &Error{Code: HistoryUnavailable, Message: "MongoDB history is unavailable", Cause: cause}
	require.ErrorIs(t, err, cause)
	require.Equal(t, "HISTORY_UNAVAILABLE: MongoDB history is unavailable", err.Error())
	require.NotContains(t, err.Error(), cause.Error())
	require.Nil(t, (&Error{Code: InvalidCheckpoint}).Unwrap())
}

func TestMongoCheckpointRejectsMalformedNestedToken(t *testing.T) {
	token, err := bson.Marshal(bson.D{{Key: "nested", Value: bson.D{{Key: "value", Value: true}}}})
	require.NoError(t, err)
	nested := bson.Raw(token).Lookup("nested").Document()
	nested[len(nested)-1] = 1
	require.NoError(t, bson.Raw(token).Validate(), "the driver only validates the outer document")
	_, err = EncodeMongo(testSource(), token)
	requireCode(t, err, InvalidCheckpoint)
}
