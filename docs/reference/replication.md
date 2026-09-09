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
        "version": 1 // optional version precondition; not stored metadata
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

### Version Preconditions

`document.version` is optional and case-sensitive. It is a precondition on the
existing live-target write path; storage assigns the resulting document version.
It does not replace server-managed metadata.

| JSON value | Behavior |
|------------|----------|
| Field omitted | No version precondition |
| Integer literal from `0` through `9223372036854775807` | Preserve the exact value as a version equality precondition |
| Null, string, boolean, negative value, fraction, exponent notation, or out-of-range integer | HTTP 400 before any change in the request reaches the Engine |

For an existing live target, a stale version returns the server document in
`conflicts`; a matching version proceeds subject to the atomic write predicate
and other storage outcomes. Explicit zero is preserved and does not mean
insert-only. The existing `create` example with version 1 remains accepted.
Omitting the version retains unconditional behavior. Invalid-version rejection
covers the whole request, but a valid batch is not transactional.

Current limits: a target missing from the initial read, including a deleted target,
can still enter Create before version checking. Concurrent deletion or a failed
conflict read can also leave incomplete conflict results. Strict insert-only
create, missing/tombstone conflicts, and structured conflict reasons remain
[proposed](../../.agents/notes/proposed/bug-fix/2026-09-07-replication-push-version-checks.md).
The [HTTP precondition decision](../../.agents/notes/implemented/bug-fix/2026-09-07-http-push-version-preconditions.md)
records the delivered fix and its limits.

## Validation & Errors
- 400: missing/invalid collection, checkpoint, action, document.id, or supplied document.version
- 409: (reserved) explicit conflict signaling; currently conflicts are returned in 200 with `conflicts`
- 500: server error

## Notes
- Document fields are flattened; do not send storage-layer fields like `_id`, `fullpath`, or `parent`.
- Checkpoint must be persisted by the client and reused for the next pull.
- Deleted docs are expressed via `deleted: true` in pull responses; identity and metadata remain, while former business fields are cleared.
- Apply tombstones before saving progress. Physical cleanup is not another business deletion and removes the tombstone from document scans. See [deletion semantics](../design/server/core/storage/03.stores.md#document-deletion-and-physical-cleanup).
