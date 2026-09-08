# Replication API Reference

Replication endpoints for offline-first clients. All documents use a flattened shape; storage internals are never exposed.

## Pull Changes
- **Endpoint:** `GET /replication/v1/pull`
- **Query Parameters:**
  - `collection` (string, required)
  - `checkpoint` (string, required): stringified int64, opaque to clients
  - `limit` (int, optional): 0–1000
- **Response 200:**
```json
{
  "documents": [
    {
      "id": "msg-2",
      "text": "Offline message",
      "version": 2,
      "updatedAt": 1710000000000,
      "createdAt": 1700000000000,
      "collection": "rooms/room-1/messages",
      "deleted": false
    }
  ],
  "checkpoint": "100"
}
```

## Push Changes
- **Endpoint:** `POST /replication/v1/push`
- **Request Body:**
```json
{
  "collection": "rooms/room-1/messages",
  "changes": [
    {
      "action": "create", // "create" | "update" | "delete"
      "document": {
        "id": "msg-2",
        "text": "Offline message",
        "version": 1 // optional optimistic concurrency hint
      }
    },
    {
      "action": "delete",
      "document": { "id": "msg-3" }
    }
  ]
}
```
- **Response 200 (conflicts only):**
```json
{
  "conflicts": [
    {
      "id": "msg-2",
      "text": "Server copy",
      "version": 3,
      "updatedAt": 1710000001000,
      "createdAt": 1700000000000,
      "collection": "rooms/room-1/messages"
    }
  ]
}
```

## Validation & Errors
- 400: missing/invalid collection, checkpoint, action, or document.id
- 409: (reserved) explicit conflict signaling; currently conflicts are returned in 200 with `conflicts`
- 500: server error

## Deletion and retention

A Syntrix document deletion is represented by a retained document with
`deleted: true`. Its public identity, collection, version, and timestamps identify
the state the client must remove or mark deleted; the former business fields
have been cleared. For example:

```json
{
  "id": "msg-3",
  "collection": "rooms/room-1/messages",
  "version": 4,
  "updatedAt": 1710000001000,
  "createdAt": 1700000000000,
  "deleted": true
}
```

| Boundary | Client-visible rule |
| --- | --- |
| Completion | Apply the tombstone before saving the response checkpoint |
| Former data | A tombstone is not a before-image of deleted business fields |
| Physical cleanup | No second business deletion is generated |
| Physically removed tombstone | A document scan cannot recover it; finite retention limits recovery of old client state |

The [storage deletion contract](../design/server/core/storage/03.stores.md#document-deletion-and-physical-cleanup)
owns cleanup prerequisites and the distinction between source and business events.

## Notes
- Document fields are flattened; do not send storage-layer fields like `_id`, `fullpath`, or `parent`.
- Checkpoint must be persisted by the client and reused for the next pull.
- Apply live documents and retained tombstones using the same document identity.
