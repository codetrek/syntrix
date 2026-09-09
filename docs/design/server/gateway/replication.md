# Replication Design

This document details the replication HTTP protocol used by Syntrix. It separates the client-visible contract from internal storage structures to avoid leaking backend details.

## Goals

- Support RxDB-style pull/push replication over HTTP.
- Keep the wire format storage-agnostic (no internal IDs, fullpaths, parents).
- Provide deterministic checkpointing using opaque stringified int64 values.
- Surface conflicts without exposing storage internals.

## Endpoint Summary

- Pull: `GET /replication/v1/pull?collection=...&checkpoint=...&limit=...`
- Push: `POST /replication/v1/push`

## Document Shape (Flattened)

Documents in responses and requests use a flattened JSON object with reserved metadata fields:

- `id` (string): required document ID.
- `version` (int64): optional version precondition on push; server-owned version on pull/conflicts.
- `updatedAt` (int64, millis): server update timestamp (returned on pull/conflicts).
- `createdAt` (int64, millis): server creation timestamp (returned on pull/conflicts).
- `collection` (string): collection path (returned on pull/conflicts).
- `deleted` (bool): present and true if the document is a tombstone.
- All other fields are user data.

## Pull

- Method: `GET /replication/v1/pull`
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
  - Deleted docs are represented via `deleted: true`; identity and metadata remain, while former business fields are cleared.
  - Physical cleanup does not create another business deletion; a removed tombstone is unavailable to document scans. See [deletion semantics](../core/storage/03.stores.md#document-deletion-and-physical-cleanup).

## Push

- Method: `POST /replication/v1/push`
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
  - `document.version` is optional and case-sensitive; preserve its exact nonnegative int64 integer value and presence as the version precondition before stripping protected metadata.
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

### Version Preconditions

| Supplied `document.version` | Request handling |
|----------------------------|------------------|
| Omitted | Preserve an absent precondition |
| Nonnegative int64 integer literal, including zero | Forward the exact value separately from document data |
| Null, string, boolean, negative value, fraction, exponent notation, or out-of-range integer | Reject the request before any Engine call |

Extract the reserved field from raw JSON so values beyond floating-point integer
precision remain exact. Ordinary business numbers keep their existing decoding
behavior. Protected fields are still removed from document data, and new stored
documents retain server initialization at version 1. The supplied value is not
assigned to stored version metadata. The existing gRPC encoding preserves absence
as `-1` and retains explicit zero and supported positive int64 values.

Push requests the database's write source for its initial and conflict lookups.
Non-not-found conflict-read errors propagate as server errors. These reads do not
lock data or establish transactions or linearizable reads. Existing live-target
writes compare the version and apply it in the atomic write predicate. Explicit zero is an equality precondition, not an insert-only request;
`create` with version 1 remains accepted. Omission retains the current optional,
unconditional behavior. A malformed version anywhere in a batch prevents all
Engine calls for that request; valid batches remain nontransactional.

The [HTTP precondition decision](../../../../.agents/notes/implemented/bug-fix/2026-09-07-http-push-version-preconditions.md)
records why extraction is local to replication decoding and preserves the
existing document-number representation.

## Checkpointing

- Clients treat checkpoint as an opaque stringified int64.
- Server guarantees monotonic increase; clients should persist the latest returned value.
- On initial sync, clients typically use `checkpoint=0`.

## Conflict Handling

- Push may return `conflicts` containing the authoritative server documents in flattened form.
- Clients decide whether to retry, merge, or surface conflicts.
- The initial not-found path can enter Create before checking the version, including
  for deleted targets. Concurrent deletion can still leave incomplete conflict
  results when the authoritative lookup reports absence. Strict create/update/delete predicates, authoritative
  missing/tombstone results, and structured conflict reasons remain in the
  [version-check proposal](../../../../.agents/notes/proposed/bug-fix/2026-09-07-replication-push-version-checks.md).

## Error Handling

- 400: validation failures (missing collection/id, invalid checkpoint, invalid action, invalid supplied document.version).
- 409: (future) may be used for explicit conflict signaling; currently conflicts are returned in 200 with the `conflicts` array.
- 500: server errors.

## Notes for Implementers

- Do not include storage-internal fields in any response or request validation.
- Keep batch sizes modest (100–500) for IndexedDB performance when using RxDB Dexie.
- Deleted docs should still include `id`, `version`, and timestamps so clients can cleanly tombstone or purge.
