# Document Definition

## 1 Document (Internal Storage Type)

This is an internal type, visible only to the storage layer. Its usage should be strictly limited to the storage layer.

```go

// Document represents a stored document in the database
type Document struct {
	// Id is the unique identifier for the document, 128-bit BLAKE3 of fullpath, binary, (e.g., blake3("chats/chatroom-1/members/alice")[:16])
	Id string `json:"id" bson:"_id"`

  // Fullpath is the Full Pathname of document, "<collection>/<document>" , e.g., chats/chatroom-1/members/alice
  Fullpath string `json:"fullpath" bson:"fullpath"`

	// Collection is the collection with fullpath, e.g., chats/chatroom-1/members
	Collection string `json:"collection" bson:"collection"`

  // Parent is the parent of collection, e.g., chats/chatroom-1
  Parent string `json:"parent" bson:"parent"`

	// Data is the actual content of the document
	Data map[string]interface{} `json:"data" bson:"data"`

	// UpdatedAt is the timestamp of the last update (Unix millionseconds), Updated on every write
	UpdatedAt int64 `json:"updatedAt" bson:"updatedAt"`

	// CreatedAt is the timestamp of the creation (Unix millionseconds), Set on create only
	CreatedAt int64 `json:"createdAt" bson:"createdAt"`

	// Version is the optimistic concurrency control version, Auto-increment per update, client cannot set
	Version int64 `json:"version" bson:"version"`

	// Deleted indicates if the document is soft-deleted
	Deleted bool `json:"deleted,omitempty" bson:"deleted,omitempty"`
}
```

## 2 User-Facing Document (Business Layer Type)

This is the business layer Document type, visible to the API.

```go
type Document map[string]interface{}
```

```json
{
  "id": "alice",               // Required; immutable once written; allowed charset: [A-Za-z0-9_.-]
  "collection": "chats/chatroom-1/members", // Shadow field, server-written; client input ignored
  "version": 0,                // Shadow field, server-written; client input ignored
  "updatedAt": 1700000000000,  // Shadow field, server-written; client input ignored
  "createdAt": 1700000000000,  // Shadow field, server-written; client input ignored
  /* other user fields */
}
```

**Rules & protections**
- `id` is required and immutable after creation; server enforces charset `[A-Za-z0-9_.-]` and rejects mutations on update/patch.
- Shadow fields (`collection`, `version`, `updatedAt`, `createdAt`) are server-owned; client-supplied values are ignored/overwritten.

## 3 Soft Delete Mechanism

When a document is deleted via the API or Storage interface, it is **soft deleted** instead of being immediately removed from the database.

1.  **Marked as Deleted**: The `deleted` field is set to `true`.
2.  **Data Cleared**: The `data` field is cleared (set to empty) to save space and ensure privacy.
3.  **Expiration**: A `sys_expires_at` field is set based on the configured retention period (default 30 days). MongoDB's TTL index will automatically remove the document after this time.
4.  **Visibility**: Soft-deleted documents are excluded from `Get` and `Query` operations by default.
5.  **Re-creation**: If a document is created with the same ID as a soft-deleted one, the soft-deleted document is overwritten (revived).
6.  **Watch Events**:
    *   Soft deletion emits a `delete` event.
    *   Reviving a soft-deleted document emits a `create` event.

- `version` in MongoDocument is incremented automatically per write and not client-settable.
- `createdAt` is set once on create; `updatedAt` updates on every write.
