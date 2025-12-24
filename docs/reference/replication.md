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

## Notes
- Document fields are flattened; do not send storage-layer fields like `_id`, `fullpath`, or `parent`.
- Checkpoint must be persisted by the client and reused for the next pull.
- Deleted docs are expressed via `deleted: true` in pull responses.
