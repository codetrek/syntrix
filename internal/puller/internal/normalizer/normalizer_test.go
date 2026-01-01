package normalizer

import (
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/puller/events"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestDefaultTenantExtractor(t *testing.T) {
	t.Parallel()
	extractor := &DefaultTenantExtractor{}

	tests := []struct {
		name       string
		fullDoc    bson.M
		docKey     bson.M
		collection string
		want       string
	}{
		{
			name:       "tenantId in fullDoc",
			fullDoc:    bson.M{"tenantId": "tenant-1"},
			docKey:     nil,
			collection: "test",
			want:       "tenant-1",
		},
		{
			name:       "_tenant in fullDoc",
			fullDoc:    bson.M{"_tenant": "tenant-2"},
			docKey:     nil,
			collection: "test",
			want:       "tenant-2",
		},
		{
			name:       "tenantId in docKey",
			fullDoc:    nil,
			docKey:     bson.M{"tenantId": "tenant-3"},
			collection: "test",
			want:       "tenant-3",
		},
		{
			name:       "default when no tenant",
			fullDoc:    bson.M{"other": "field"},
			docKey:     bson.M{"_id": "123"},
			collection: "test",
			want:       "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractor.Extract(tt.fullDoc, tt.docKey, tt.collection)
			if got != tt.want {
				t.Errorf("Extract() = %q, want %q", got, tt.want)
			}
		})
	}
}

func makeRawEvent(opType string, clusterT, clusterI uint32, fullDoc bson.M, docKey bson.M, db, coll string) *RawEvent {
	raw := &RawEvent{
		OperationType: opType,
		ClusterTime:   primitive.Timestamp{T: clusterT, I: clusterI},
		FullDocument:  fullDoc,
		DocumentKey:   docKey,
	}
	raw.Namespace.DB = db
	raw.Namespace.Coll = coll
	return raw
}

func TestNormalizer_Normalize(t *testing.T) {
	t.Parallel()
	n := New(nil)

	tests := []struct {
		name    string
		raw     *RawEvent
		wantErr bool
		check   func(*testing.T, *events.NormalizedEvent)
	}{
		{
			name: "insert operation",
			raw: makeRawEvent(
				"insert",
				1234567890, 1,
				bson.M{"_id": "doc1", "name": "test"},
				bson.M{"_id": "doc1"},
				"testdb", "testcoll",
			),
			wantErr: false,
			check: func(t *testing.T, evt *events.NormalizedEvent) {
				if evt.Type != events.OperationInsert {
					t.Errorf("Type = %s, want insert", evt.Type)
				}
				if evt.DocumentID != "doc1" {
					t.Errorf("DocumentID = %s, want doc1", evt.DocumentID)
				}
				if evt.Collection != "testcoll" {
					t.Errorf("Collection = %s, want testcoll", evt.Collection)
				}
			},
		},
		{
			name: "update operation",
			raw: makeRawEvent(
				"update",
				1234567890, 2,
				bson.M{"_id": "doc2", "name": "updated"},
				bson.M{"_id": "doc2"},
				"testdb", "testcoll",
			),
			wantErr: false,
			check: func(t *testing.T, evt *events.NormalizedEvent) {
				if evt.Type != events.OperationUpdate {
					t.Errorf("Type = %s, want update", evt.Type)
				}
			},
		},
		{
			name: "delete operation",
			raw: makeRawEvent(
				"delete",
				1234567890, 3,
				nil,
				bson.M{"_id": "doc3"},
				"testdb", "testcoll",
			),
			wantErr: false,
			check: func(t *testing.T, evt *events.NormalizedEvent) {
				if evt.Type != events.OperationDelete {
					t.Errorf("Type = %s, want delete", evt.Type)
				}
				if evt.FullDocument != nil {
					t.Error("FullDocument should be nil for delete")
				}
			},
		},
		{
			name: "unknown operation type",
			raw: makeRawEvent(
				"unknown",
				1234567890, 1,
				nil,
				bson.M{"_id": "doc1"},
				"testdb", "testcoll",
			),
			wantErr: true,
		},
		{
			name: "ObjectID document key",
			raw: func() *RawEvent {
				oid := primitive.NewObjectID()
				return makeRawEvent(
					"insert",
					1234567890, 1,
					bson.M{"_id": oid, "name": "test"},
					bson.M{"_id": oid},
					"testdb", "testcoll",
				)
			}(),
			wantErr: false,
			check: func(t *testing.T, evt *events.NormalizedEvent) {
				if evt.DocumentID == "" {
					t.Error("DocumentID should not be empty")
				}
			},
		},
		{
			name: "With TxnNumber",
			raw: func() *RawEvent {
				raw := makeRawEvent(
					"insert",
					1234567890, 1,
					bson.M{"_id": "doc1"},
					bson.M{"_id": "doc1"},
					"testdb", "testcoll",
				)
				txn := int64(100)
				raw.TxnNumber = &txn
				return raw
			}(),
			wantErr: false,
			check: func(t *testing.T, evt *events.NormalizedEvent) {
				if evt.TxnNumber == nil || *evt.TxnNumber != 100 {
					t.Error("TxnNumber not preserved")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt, err := n.Normalize(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("Normalize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				tt.check(t, evt)
			}
		})
	}
}

func TestNormalizer_ConvertBsonValue(t *testing.T) {
	t.Parallel()
	n := New(nil)

	raw := makeRawEvent(
		"insert",
		1234567890, 1,
		bson.M{
			"_id":       "doc1",
			"name":      "test",
			"count":     int32(42),
			"price":     3.14,
			"active":    true,
			"tags":      bson.A{"a", "b", "c"},
			"nested":    bson.M{"key": "value"},
			"createdAt": primitive.NewDateTimeFromTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
		bson.M{"_id": "doc1"},
		"testdb", "testcoll",
	)

	evt, err := n.Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if evt.FullDocument == nil {
		t.Fatal("FullDocument is nil")
	}

	// Check nested map
	nested, ok := evt.FullDocument["nested"].(map[string]any)
	if !ok {
		t.Error("nested should be map[string]any")
	} else if nested["key"] != "value" {
		t.Errorf("nested.key = %v, want 'value'", nested["key"])
	}

	// Check array
	tags, ok := evt.FullDocument["tags"].([]any)
	if !ok {
		t.Error("tags should be []any")
	} else if len(tags) != 3 {
		t.Errorf("len(tags) = %d, want 3", len(tags))
	}

	// Check DateTime conversion
	createdAt, ok := evt.FullDocument["createdAt"].(time.Time)
	if !ok {
		t.Errorf("createdAt should be time.Time, got %T", evt.FullDocument["createdAt"])
	} else if createdAt.Year() != 2024 {
		t.Errorf("createdAt.Year() = %d, want 2024", createdAt.Year())
	}
}

func TestNormalizer_UpdateDescription(t *testing.T) {
	t.Parallel()
	n := New(nil)

	raw := &RawEvent{
		OperationType: "update",
		ClusterTime:   primitive.Timestamp{T: 1234567890, I: 1},
		DocumentKey:   bson.M{"_id": "doc1"},
		FullDocument:  bson.M{"_id": "doc1", "name": "updated"},
		UpdateDescription: bson.M{
			"updatedFields": bson.M{"name": "updated"},
			"removedFields": bson.A{"oldField"},
			"truncatedArrays": bson.A{
				bson.M{"field": "items", "newSize": int32(10)},
			},
		},
	}
	raw.Namespace.DB = "testdb"
	raw.Namespace.Coll = "testcoll"

	evt, err := n.Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if evt.UpdateDesc == nil {
		t.Fatal("UpdateDesc is nil")
	}

	if evt.UpdateDesc.UpdatedFields["name"] != "updated" {
		t.Errorf("UpdatedFields.name = %v, want 'updated'", evt.UpdateDesc.UpdatedFields["name"])
	}

	if len(evt.UpdateDesc.RemovedFields) != 1 || evt.UpdateDesc.RemovedFields[0] != "oldField" {
		t.Errorf("RemovedFields = %v, want ['oldField']", evt.UpdateDesc.RemovedFields)
	}

	if len(evt.UpdateDesc.TruncatedArrays) != 1 {
		t.Errorf("len(TruncatedArrays) = %d, want 1", len(evt.UpdateDesc.TruncatedArrays))
	} else {
		ta := evt.UpdateDesc.TruncatedArrays[0]
		if ta.Field != "items" || ta.NewSize != 10 {
			t.Errorf("TruncatedArray = %+v, want {Field: items, NewSize: 10}", ta)
		}
	}
}

func TestExtractDocumentID_StringID(t *testing.T) {
	t.Parallel()
	docKey := bson.M{"_id": "my-string-id"}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	if id != "my-string-id" {
		t.Errorf("extractDocumentID() = %q, want 'my-string-id'", id)
	}
}

func TestExtractDocumentID_ObjectID(t *testing.T) {
	t.Parallel()
	oid := primitive.NewObjectID()
	docKey := bson.M{"_id": oid}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	if id != oid.Hex() {
		t.Errorf("extractDocumentID() = %q, want %q", id, oid.Hex())
	}
}

func TestExtractDocumentID_IntID(t *testing.T) {
	t.Parallel()
	docKey := bson.M{"_id": int64(12345)}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	if id != "12345" {
		t.Errorf("extractDocumentID() = %q, want '12345'", id)
	}
}

func TestExtractDocumentID_Int32ID(t *testing.T) {
	t.Parallel()
	docKey := bson.M{"_id": int32(999)}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	if id != "999" {
		t.Errorf("extractDocumentID() = %q, want '999'", id)
	}
}

func TestExtractDocumentID_IntPlainID(t *testing.T) {
	docKey := bson.M{"_id": int(42)}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	if id != "42" {
		t.Errorf("extractDocumentID() = %q, want '42'", id)
	}
}

func TestExtractDocumentID_OtherType(t *testing.T) {
	docKey := bson.M{"_id": 3.14}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	// Should use %v formatting
	if id == "" {
		t.Error("extractDocumentID() returned empty string")
	}
}

func TestExtractDocumentID_NilDocKey(t *testing.T) {
	_, err := extractDocumentID(nil)
	if err == nil {
		t.Error("extractDocumentID(nil) should return error")
	}
}

func TestExtractDocumentID_CompoundKey(t *testing.T) {
	// No _id field, should use compound key formatting
	docKey := bson.M{"field1": "value1", "field2": "value2"}
	id, err := extractDocumentID(docKey)
	if err != nil {
		t.Fatalf("extractDocumentID() error = %v", err)
	}
	// Should return a hash of the document key
	if id == "" {
		t.Error("extractDocumentID() returned empty string for compound key")
	}
	// Hash should be 32 hex characters (16 bytes)
	if len(id) != 32 {
		t.Errorf("compound key hash length = %d, want 32", len(id))
	}
}

func TestFormatCompoundKey(t *testing.T) {
	// Use single-key map to avoid non-deterministic map iteration order
	docKey := bson.M{"key": "value"}
	result := formatCompoundKey(docKey)

	if result == "" {
		t.Error("formatCompoundKey() returned empty string")
	}
	// Should be a hex string (32 chars for 16 bytes)
	if len(result) != 32 {
		t.Errorf("formatCompoundKey() length = %d, want 32", len(result))
	}

	// Same single-key input should produce same output
	docKey2 := bson.M{"key": "value"}
	result2 := formatCompoundKey(docKey2)
	if result != result2 {
		t.Error("formatCompoundKey() should be deterministic for same input")
	}
}

func TestFormatID(t *testing.T) {
	tests := []struct {
		name string
		id   any
		want string
	}{
		{
			name: "ObjectID",
			id:   primitive.NewObjectIDFromTimestamp(time.Unix(1234567890, 0)),
			want: "", // Will check it's not empty
		},
		{
			name: "string",
			id:   "my-id",
			want: "my-id",
		},
		{
			name: "int",
			id:   123,
			want: "123",
		},
		{
			name: "int32",
			id:   int32(456),
			want: "456",
		},
		{
			name: "int64",
			id:   int64(789),
			want: "789",
		},
		{
			name: "float",
			id:   3.14,
			want: "3.14",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatID(tt.id)
			if tt.want == "" {
				// Just check it's not empty
				if result == "" {
					t.Error("formatID() returned empty string")
				}
			} else if result != tt.want {
				t.Errorf("formatID() = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestNormalizer_NilDocumentKey(t *testing.T) {
	n := New(nil)

	raw := &RawEvent{
		OperationType: "insert",
		ClusterTime:   primitive.Timestamp{T: 1234567890, I: 1},
		DocumentKey:   nil, // nil document key
		FullDocument:  bson.M{"_id": "doc1", "name": "test"},
	}
	raw.Namespace.DB = "testdb"
	raw.Namespace.Coll = "testcoll"

	_, err := n.Normalize(raw)
	if err == nil {
		t.Error("Normalize() should fail with nil DocumentKey")
	}
}

func TestParseEventID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		id      string
		wantT   uint32
		wantI   uint32
		wantErr bool
	}{
		{
			name:    "valid id",
			id:      "1234567890-1-abcdef12",
			wantT:   1234567890,
			wantI:   1,
			wantErr: false,
		},
		{
			name:    "invalid format",
			id:      "invalid",
			wantErr: true,
		},
		{
			name:    "missing hash",
			id:      "1234567890-1",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct, err := ParseEventID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEventID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ct.T != tt.wantT {
					t.Errorf("ParseEventID() T = %v, want %v", ct.T, tt.wantT)
				}
				if ct.I != tt.wantI {
					t.Errorf("ParseEventID() I = %v, want %v", ct.I, tt.wantI)
				}
			}
		})
	}
}
