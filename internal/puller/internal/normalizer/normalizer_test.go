package normalizer

import (
	"testing"
	"time"

	"github.com/codetrek/syntrix/internal/puller/events"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

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
	n := New()

	tests := []struct {
		name    string
		raw     *RawEvent
		wantErr bool
		check   func(*testing.T, *events.ChangeEvent)
	}{
		{
			name: "insert operation",
			raw: makeRawEvent(
				"insert",
				1234567890, 1,
				bson.M{"_id": "doc1", "tenant_id": "tenant-1", "fullpath": "/path"},
				bson.M{"_id": "doc1"},
				"testdb", "testcoll",
			),
			wantErr: false,
			check: func(t *testing.T, evt *events.ChangeEvent) {
				if evt.OpType != events.OperationInsert {
					t.Errorf("OpType = %s, want insert", evt.OpType)
				}
				if evt.MgoDocID != "doc1" {
					t.Errorf("MgoDocID = %s, want doc1", evt.MgoDocID)
				}
				if evt.MgoColl != "testcoll" {
					t.Errorf("MgoColl = %s, want testcoll", evt.MgoColl)
				}
				if evt.TenantID != "tenant-1" {
					t.Errorf("TenantID = %s, want tenant-1", evt.TenantID)
				}
				if evt.FullDocument == nil {
					t.Error("FullDocument should not be nil")
				} else if evt.FullDocument.Id != "doc1" {
					t.Errorf("FullDocument.Id = %s, want doc1", evt.FullDocument.Id)
				}
			},
		},
		{
			name: "update operation with fullDocument",
			raw: makeRawEvent(
				"update",
				1234567890, 2,
				bson.M{"_id": "doc2", "tenant_id": "tenant-2", "fullpath": "/path2"},
				bson.M{"_id": "doc2"},
				"testdb", "testcoll",
			),
			wantErr: false,
			check: func(t *testing.T, evt *events.ChangeEvent) {
				if evt.OpType != events.OperationUpdate {
					t.Errorf("OpType = %s, want update", evt.OpType)
				}
				if evt.TenantID != "tenant-2" {
					t.Errorf("TenantID = %s, want tenant-2", evt.TenantID)
				}
			},
		},
		{
			name: "update operation missing fullDocument",
			raw: makeRawEvent(
				"update",
				1234567890, 2,
				nil,
				bson.M{"_id": "doc2"},
				"testdb", "testcoll",
			),
			wantErr: true, // Should fail
			check:   nil,
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
			check: func(t *testing.T, evt *events.ChangeEvent) {
				if evt.OpType != events.OperationDelete {
					t.Errorf("OpType = %s, want delete", evt.OpType)
				}
				if evt.FullDocument != nil {
					t.Error("FullDocument should be nil for delete")
				}
				if evt.TenantID != "" {
					t.Errorf("TenantID should be empty for delete, got %s", evt.TenantID)
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
					bson.M{"_id": oid, "tenant_id": "t1", "fullpath": "/p"},
					bson.M{"_id": oid},
					"testdb", "testcoll",
				)
			}(),
			wantErr: false,
			check: func(t *testing.T, evt *events.ChangeEvent) {
				if evt.MgoDocID == "" {
					t.Error("MgoDocID should not be empty")
				}
			},
		},
		{
			name: "With TxnNumber",
			raw: func() *RawEvent {
				raw := makeRawEvent(
					"insert",
					1234567890, 1,
					bson.M{"_id": "doc1", "tenant_id": "t1", "fullpath": "/p"},
					bson.M{"_id": "doc1"},
					"testdb", "testcoll",
				)
				txn := int64(100)
				raw.TxnNumber = &txn
				return raw
			}(),
			wantErr: false,
			check: func(t *testing.T, evt *events.ChangeEvent) {
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

func TestNormalizer_UpdateDescription(t *testing.T) {
	t.Parallel()
	n := New()

	raw := &RawEvent{
		OperationType: "update",
		ClusterTime:   primitive.Timestamp{T: 1234567890, I: 1},
		DocumentKey:   bson.M{"_id": "doc1"},
		FullDocument:  bson.M{"_id": "doc1", "tenant_id": "t1", "fullpath": "/p"},
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
	n := New()

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

func TestConvertBsonValue(t *testing.T) {
	t.Parallel()

	oid := primitive.NewObjectID()
	now := time.Now().UTC().Truncate(time.Millisecond)
	ts := primitive.Timestamp{T: 123, I: 456}

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{
			name:  "string",
			input: "test",
			want:  "test",
		},
		{
			name:  "int",
			input: 123,
			want:  123,
		},
		{
			name:  "bson.M",
			input: bson.M{"key": "value"},
			want:  map[string]any{"key": "value"},
		},
		{
			name:  "bson.A",
			input: bson.A{"item1", 123},
			want:  []any{"item1", 123},
		},
		{
			name:  "ObjectID",
			input: oid,
			want:  oid.Hex(),
		},
		{
			name:  "DateTime",
			input: primitive.NewDateTimeFromTime(now),
			want:  now.Local(),
		},
		{
			name:  "Timestamp",
			input: ts,
			want:  map[string]uint32{"T": 123, "I": 456},
		},
		{
			name: "Nested",
			input: bson.M{
				"array": bson.A{
					bson.M{"nested": "value"},
				},
			},
			want: map[string]any{
				"array": []any{
					map[string]any{"nested": "value"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertBsonValue(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
