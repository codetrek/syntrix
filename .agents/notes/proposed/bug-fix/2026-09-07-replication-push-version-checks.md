# Agent Note: Preserve Replication Push Version Preconditions

Status: proposed

## Problem

The [HTTP precondition repair](../../implemented/bug-fix/2026-09-07-http-push-version-preconditions.md)
now preserves exact optional versions before protected fields are stripped,
restoring existing live-target checks. The remaining gaps prevent complete
conditional-write safety.

The [Query engine](../../../../internal/query/core/engine.go) takes its
not-found/Create branch before checking `BaseVersion`.
[Mongo Get](../../../../internal/core/storage/mongo/document_store.go) excludes
deleted documents, while Create can replace a tombstone. A stale conditional
update or delete can therefore recreate a missing or deleted target. Concurrent
deletion and ignored conflict-read errors can also produce incomplete conflict
results. The document-only conflict array cannot represent actual absence.

## Proposal

Preserve the delivered exact version value, optionality, protected-field stripping,
and storage-owned resulting versions. Extend the local and gRPC contracts to
preserve the requested create/update/delete action and define the following
conditional semantics. These are proposed rules, not current behavior: the
current API accepts `create` with version 1, and zero is an ordinary live-target
equality precondition rather than an insert-only instruction.

- An absent version preserves the documented optional, unconditional-write policy.
- `create` with version zero means insert only if no live document or retained
  tombstone exists. The absence check and insert must be atomic; this path cannot
  use tombstone-replacing Create.
- `update` or `delete` with a positive version requires a live document at that
  version. A missing target, tombstone, or different version is a conflict and
  never enters Create.
- Zero on update/delete and a positive version on create are invalid combinations.
  Conditional resurrection of a retained tombstone is not implicit in any action.

Apply these predicates in the write itself, including races after an initial read.
Resolve failed predicates through a database-scoped authoritative read that
includes tombstones. Return real tombstone metadata when available; report actual
absence without inventing a document. If that read fails, surface its error.

Make conflicts structured entries containing document ID, a reason
(`version_mismatch`, `missing`, `tombstoned`, or `already_exists`), and the current
document when one exists. This replaces the document-only conflict array so
absent targets are representable. Update HTTP, protobuf, internal types, SDK
consumers, and reference examples in a coordinated protocol migration. Preserve
optionality explicitly; old gRPC clients must not have omitted action/version
fields reinterpreted as create/version-zero. Do not add a silent compatibility
fallback.

Keep per-change batch outcomes without claiming batch atomicity. Record request
identity and conflict/error counts without document bodies. The HTTP extraction
repair leaves these changes deferred: closing the remaining gaps requires
Query/storage changes and the coordinated protocol work described here.

## Alternatives

**Introduce a separate public `baseVersion` field.** This separates metadata from
payload clearly, but changes the established flattened protocol when its existing
version field already has the necessary meaning.

**Require versions for every push.** This strengthens unconditional-write
protection but changes documented optional behavior and needs a separate
client-contract decision.

**Represent missing targets as fabricated tombstones.** This preserves the old
conflict-array shape, but invents authoritative version and deletion metadata.
Structured conflict outcomes preserve the distinction between absence and a
retained tombstone.

## Acceptance Criteria

- Preserve the delivered HTTP behavior: matching live-target versions can proceed,
  exact optional int64 values survive local/gRPC routing, malformed versions fail
  before writes, and storage assigns resulting versions.
- A stale conditional update/delete after concurrent soft deletion returns the
  authoritative tombstone; after physical removal it returns `missing`. Neither
  recreates the target.
- Version-zero create inserts a truly absent target; concurrent create, existing
  live data, and retained tombstones produce explicit conflicts without overwrites.
- Absent versions retain documented behavior; the proposed invalid action/version
  combinations fail before writes once their new contract is implemented.
- Local and gRPC routes preserve action and optionality; failed conflict reads
  return errors, and old transport messages cannot become unintended conditional
  creates.
- Mixed batches and multiple databases cannot lose a precondition or report a
  conflict for another document.

## Risks

The structured conflict response and explicit transport presence require
synchronized client changes. Version precision must remain exact. Tombstone
expiration removes deletion history, so a genuinely absent target can later
satisfy the proposed version-zero create. A lost success response may produce a
conflict on retry; these changes do not introduce exactly-once execution.

## Dependencies

[SDK offline replication](../feature/2026-09-07-sdk-offline-replication.md) consumes
the proposed corrected conflict contract. The
[replication reference](../../../../docs/reference/replication.md#version-preconditions)
records the delivered input contract and current safety limits.
