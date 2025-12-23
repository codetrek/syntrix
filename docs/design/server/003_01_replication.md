# Replication Design

This document details the replication HTTP protocol used by Syntrix. It separates the client-visible contract from internal storage structures to avoid leaking backend details.

## Goals
- Support RxDB-style pull/push replication over HTTP.
- Keep the wire format storage-agnostic (no internal IDs, fullpaths, parents).
- Provide deterministic checkpointing using opaque stringified int64 values.
- Surface conflicts without exposing storage internals.

## Endpoint Summary
- Pull: `GET /v1/replication/pull?collection=...&checkpoint=...&limit=...`
- Push: `POST /v1/replication/push`

## Document Shape (Flattened)
Documents in responses and requests use a flattened JSON object with reserved metadata fields:
- `id` (string): required document ID.
- `version` (int64): optimistic concurrency version (optional on push, returned on pull/conflicts).
- `updatedAt` (int64, millis): server update timestamp (returned on pull/conflicts).
- `createdAt` (int64, millis): server creation timestamp (returned on pull/conflicts).
- `collection` (string): collection path (returned on pull/conflicts).
- `deleted` (bool): present and true if the document is a tombstone.
- All other fields are user data.

## Pull
- Method: `GET /v1/replication/pull`
- Query params:
  - `collection` (string, required): collection path.
  - `checkpoint` (string, required): last known checkpoint as stringified int64; opaque to clients.
  - `limit` (int, optional): 0–1000.
- Response:
```json
{
  "documents": [
    {
      "id": "m1",
      "text": "hello",
      "version": 2,
      "updatedAt": 1710000000000,
      "createdAt": 1700000000000,
      "collection": "room/chatroom-1/messages",
      "deleted": false
    }
  ],
  "checkpoint": "123"
}
```
- Semantics:
  - Documents are ordered by server checkpoint (monotonic).
  - `checkpoint` in response is the new high-water mark for the next pull.
  - Deleted docs are represented via `deleted: true`; body still includes metadata.

## Push
- Method: `POST /v1/replication/push`
- Request body (flattened documents):
```json
{
  "collection": "room/chatroom-1/messages",
  "changes": [
    {
      "action": "create",
      "document": {
        "id": "m1",
        "text": "hello",
        "version": 1
      }
    },
    {
      "action": "delete",
      "document": { "id": "m2" }
    }
  ]
}
```
- Rules:
  - `action` ∈ {"create", "update", "delete"}.
  - `document.id` is required for every change.
  - `version` is optional; when provided, it is used as an optimistic concurrency hint.
  - No storage-layer fields (e.g., `_id`, `fullpath`, `parent`) are accepted or returned.
- Response (conflicts only):
```json
{
  "conflicts": [
    {
      "id": "m1",
      "text": "server-copy",
      "version": 3,
      "updatedAt": 1710000001000,
      "createdAt": 1700000000000,
      "collection": "room/chatroom-1/messages"
    }
  ]
}
```

## Checkpointing
- Clients treat checkpoint as an opaque stringified int64.
- Server guarantees monotonic increase; clients should persist the latest returned value.
- On initial sync, clients typically use `checkpoint=0`.

## Conflict Handling
- Push may return `conflicts` containing the authoritative server documents in flattened form.
- Clients decide whether to retry, merge, or surface conflicts.

## Error Handling
- 400: validation failures (missing collection/id, invalid checkpoint, invalid action).
- 409: (future) may be used for explicit conflict signaling; currently conflicts are returned in 200 with the `conflicts` array.
- 500: server errors.

## Notes for Implementers
- Do not include storage-internal fields in any response or request validation.
- Keep batch sizes modest (100–500) for IndexedDB performance when using RxDB Dexie.
- Deleted docs should still include `id`, `version`, and timestamps so clients can cleanly tombstone or purge.
