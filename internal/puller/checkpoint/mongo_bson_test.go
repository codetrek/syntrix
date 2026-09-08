package checkpoint

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func rawToken(kind bsontype.Type, value []byte) bson.Raw {
	token := append([]byte{0, 0, 0, 0, byte(kind), 'v', 0}, value...)
	token = append(token, 0)
	binary.LittleEndian.PutUint32(token, uint32(len(token)))
	return token
}

func nativeValue(t *testing.T, value any) bson.RawValue {
	t.Helper()
	kind, data, err := bson.MarshalValue(value)
	require.NoError(t, err)
	return bson.RawValue{Type: kind, Value: data}
}

func TestMongoCheckpointPreservesAllBSONValueTypes(t *testing.T) {
	for name, value := range map[string]any{
		"document":        bson.D{{Key: "z", Value: bson.D{}}, {Key: "a", Value: true}},
		"array":           bson.A{false, bson.D{{Key: "nested", Value: bson.A{int32(1), nil}}}},
		"code with scope": primitive.CodeWithScope{Code: "return x", Scope: bson.D{{Key: "x", Value: bson.A{int64(1)}}}},
		"string":          "native\x00string",
		"JavaScript":      primitive.JavaScript("return 1"),
		"symbol":          primitive.Symbol("symbol"),
		"DBPointer":       primitive.DBPointer{DB: "physical", Pointer: primitive.ObjectID{1, 2, 3}},
		"binary":          primitive.Binary{Subtype: 128, Data: []byte{0, 255, 2}},
		"old binary":      primitive.Binary{Subtype: 2, Data: []byte{0, 255, 2}},
		"regex":           primitive.Regex{Pattern: "a.*", Options: "im"},
		"false":           false,
		"true":            true,
		"int32":           int32(-3),
		"int64":           int64(-4),
		"double":          1.25,
		"date":            primitive.DateTime(-5),
		"timestamp":       primitive.Timestamp{T: 10, I: 1},
		"object ID":       primitive.ObjectID{1, 2, 3},
		"decimal":         primitive.NewDecimal128(1, 2),
		"max key":         primitive.MaxKey{},
		"min key":         primitive.MinKey{},
		"null":            primitive.Null{},
		"undefined":       primitive.Undefined{},
	} {
		t.Run(name, func(t *testing.T) {
			value := nativeValue(t, value)
			token := rawToken(value.Type, value.Value)
			original := slices.Clone(token)
			cp, err := EncodeMongo(testSource(), token)
			require.NoError(t, err)
			_, position, err := DecodeMongo(cp)
			require.NoError(t, err)
			require.Equal(t, original, position.ResumeAfter)
			require.Equal(t, original, token, "validation must not mutate native BSON")
		})
	}
}

func TestMongoCheckpointRejectsMalformedBSONContents(t *testing.T) {
	validDocument := nativeValue(t, bson.D{{Key: "x", Value: true}}).Value
	badTerminator := slices.Clone(validDocument)
	badTerminator[len(badTerminator)-1] = 1
	duplicateDocument := nativeValue(t, bson.D{{Key: "x", Value: true}, {Key: "x", Value: false}}).Value
	outOfOrderArray := nativeValue(t, bson.D{{Key: "1", Value: true}}).Value
	badArrayBoolean := nativeValue(t, bson.A{true}).Value
	badArrayBoolean[len(badArrayBoolean)-2] = 2
	badCodeScope := nativeValue(t, primitive.CodeWithScope{Code: "x", Scope: bson.D{{Key: "x", Value: true}}}).Value
	badCodeScope[len(badCodeScope)-1] = 1
	badCodeString := nativeValue(t, primitive.CodeWithScope{Code: "x", Scope: bson.D{}}).Value
	badCodeString[9] = 1
	extraCodeScope := nativeValue(t, primitive.CodeWithScope{Code: "x", Scope: bson.D{}}).Value
	extraCodeScope = append(extraCodeScope, 0)
	binary.LittleEndian.PutUint32(extraCodeScope, uint32(len(extraCodeScope)))
	badKey := slices.Clone(validDocument)
	badKey[5] = 255
	for _, test := range []struct {
		name  string
		kind  bsontype.Type
		value []byte
	}{
		{"nested terminator", bsontype.EmbeddedDocument, badTerminator},
		{"nested duplicate", bsontype.EmbeddedDocument, duplicateDocument},
		{"nested invalid key", bsontype.EmbeddedDocument, badKey},
		{"short document", bsontype.EmbeddedDocument, []byte{4, 0, 0, 0}},
		{"long document", bsontype.EmbeddedDocument, []byte{100, 0, 0, 0, 0}},
		{"array indexes", bsontype.Array, outOfOrderArray},
		{"array value", bsontype.Array, badArrayBoolean},
		{"code scope terminator", bsontype.CodeWithScope, badCodeScope},
		{"code string terminator", bsontype.CodeWithScope, badCodeString},
		{"code extra bytes", bsontype.CodeWithScope, extraCodeScope},
		{"short code", bsontype.CodeWithScope, []byte{4, 0, 0, 0}},
		{"string length missing", bsontype.String, []byte{1, 0}},
		{"string zero length", bsontype.String, []byte{0, 0, 0, 0}},
		{"string negative length", bsontype.String, []byte{255, 255, 255, 255}},
		{"string large length", bsontype.String, []byte{255, 255, 255, 127}},
		{"string terminator", bsontype.String, []byte{2, 0, 0, 0, 'x', 1}},
		{"string UTF8", bsontype.String, []byte{2, 0, 0, 0, 255, 0}},
		{"DBPointer invalid namespace", bsontype.DBPointer, []byte{0, 0, 0, 0}},
		{"DBPointer missing ID", bsontype.DBPointer, []byte{1, 0, 0, 0, 0}},
		{"binary missing subtype", bsontype.Binary, []byte{0, 0, 0, 0}},
		{"binary negative size", bsontype.Binary, []byte{255, 255, 255, 255, 0}},
		{"old binary short size", bsontype.Binary, []byte{0, 0, 0, 0, 2}},
		{"old binary nested size", bsontype.Binary, []byte{4, 0, 0, 0, 2, 1, 0, 0, 0}},
		{"regex pattern terminator", bsontype.Regex, []byte{'x'}},
		{"regex options terminator", bsontype.Regex, []byte{'x', 0, 'i'}},
		{"regex UTF8", bsontype.Regex, []byte{255, 0, 0}},
		{"regex options order", bsontype.Regex, []byte{'x', 0, 'm', 'i', 0}},
		{"regex duplicate option", bsontype.Regex, []byte{'x', 0, 'i', 'i', 0}},
		{"regex unknown option", bsontype.Regex, []byte{'x', 0, 'q', 0}},
		{"boolean byte", bsontype.Boolean, []byte{2}},
		{"boolean missing", bsontype.Boolean, nil},
		{"integer short", bsontype.Int32, []byte{1}},
		{"unknown type", bsontype.Type(254), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			token := rawToken(test.kind, test.value)
			_, err := EncodeMongo(testSource(), token)
			requireCode(t, err, InvalidCheckpoint)
			source, err := canonicalMongoSource(testSource())
			require.NoError(t, err)
			cp := encodeEnvelope(t, mongoEnvelope{Version: 1, Kind: "mongo", Capture: MongoCaptureContract, Source: source, ResumeAfter: token})
			requireCode(t, Validate(cp), InvalidCheckpoint)
		})
	}
	missingKeyTerminator := bson.Raw{7, 0, 0, 0, byte(bsontype.Null), 'v', 0}
	_, err := EncodeMongo(testSource(), missingKeyTerminator)
	requireCode(t, err, InvalidCheckpoint)
}

func TestMongoCheckpointBoundsBSONNesting(t *testing.T) {
	token := rawToken(bsontype.Null, nil)
	for range maxTokenDepth {
		token = rawToken(bsontype.EmbeddedDocument, token)
	}
	cp, err := EncodeMongo(testSource(), token)
	require.NoError(t, err)
	require.NoError(t, Validate(cp))
	token = rawToken(bsontype.EmbeddedDocument, token)
	_, err = EncodeMongo(testSource(), token)
	requireCode(t, err, InvalidCheckpoint)
}
