# Agent Note: Preserve Replication Push Version Preconditions

Status: proposed

## Problem

The [replication reference](../../../../docs/reference/replication.md) documents `document.version` as an optional optimistic-concurrency hint. The [HTTP Push handler](../../../../internal/gateway/rest/handler_replication.go) strips that field before extracting it and constructs changes without `BaseVersion`. Conditional HTTP writes therefore lose their precondition.

The [Query engine](../../../../internal/query/core/engine.go) also takes its not-found/Create branch before checking `BaseVersion`. [Mongo Get](../../../../internal/core/storage/mongo/document_store.go) excludes deleted documents, while Create can replace a tombstone. Restoring HTTP version propagation alone would still allow a stale conditional update or delete to recreate a missing or deleted target. These findings follow from static inspection.

## Proposal

Extract the supplied version before stripping protected fields. Preserve its exact integer value and presence, along with the requested create/update/delete action, through local and gRPC boundaries. Storage assigns resulting versions; a supplied version is only a precondition. Reject malformed, fractional, negative, or out-of-range versions before any writes.

Define conditional operation semantics explicitly:

- An absent version preserves the documented optional, unconditional-write policy.
- `create` with version zero means insert only if no live document or retained tombstone exists. The absence check and insert must be atomic; this path cannot use tombstone-replacing Create.
- `update` or `delete` with a positive version requires a live document at that version. A missing target, tombstone, or different version is a conflict and never enters Create.
- Zero on update/delete and a positive version on create are invalid combinations. Conditional resurrection of a retained tombstone is not implicit in any action.

Apply these predicates in the write itself, including races after an initial read. Resolve failed predicates through a database-scoped authoritative read that includes tombstones. Return real tombstone metadata when available; report actual absence without inventing a document. If that read fails, surface its error.

Make conflicts structured entries containing document ID, a reason (`version_mismatch`, `missing`, `tombstoned`, or `already_exists`), and the current document when one exists. This replaces the current document-only conflict array so absent targets are representable. Update HTTP, protobuf, internal types, SDK consumers, and reference examples in a coordinated protocol migration. Preserve optionality explicitly; old gRPC clients must not have omitted action/version fields reinterpreted as create/version-zero. Do not add a silent compatibility fallback.

Keep per-change batch outcomes without claiming batch atomicity. Record request identity and conflict/error counts without document bodies.

## Alternatives

**Introduce a separate public `baseVersion` field.** This separates metadata from payload clearly, but changes the established flattened protocol when its existing version field already has the necessary meaning.

**Require versions for every push.** This strengthens unconditional-write protection but changes the documented optional behavior and needs a separate client-contract decision.

**Represent missing targets as fabricated tombstones.** This preserves the old conflict-array shape, but invents authoritative version and deletion metadata. Structured conflict outcomes preserve the distinction between absence and a retained tombstone.

## Acceptance Criteria

- HTTP update and delete with a matching version succeed and storage advances its own version.
- A stale conditional update/delete after concurrent soft deletion returns the authoritative tombstone; after physical removal it returns `missing`. Neither recreates the target.
- Version-zero create inserts a truly absent target; concurrent create, existing live data, and retained tombstones produce explicit conflicts without overwrites.
- Absent versions retain documented behavior; malformed, out-of-range, and invalid action/version combinations fail before writes.
- Local and gRPC routes preserve action, optionality, and exact version values; failed conflict reads return errors, and old transport messages cannot become unintended conditional creates.
- Mixed batches and multiple databases cannot lose a precondition or report a conflict for another document.

## Risks

The structured conflict response and explicit transport presence require synchronized client changes. JSON number handling must not round versions. Tombstone expiration removes deletion history, so a genuinely absent target can later satisfy version-zero create. A lost success response may produce a conflict on retry; this repair does not introduce exactly-once execution.

## Dependencies

[SDK offline replication](../feature/2026-09-07-sdk-offline-replication.md) consumes this corrected conflict contract.
