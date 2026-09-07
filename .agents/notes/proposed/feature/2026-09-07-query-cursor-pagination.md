# Agent Note: Complete Query Cursor Pagination across Storage and Indexes

Status: proposed

## Problem

The public [query model](../../../../pkg/model/query.go) accepts `StartAfter`, but [Mongo Query](../../../../internal/core/storage/mongo/document_store.go) builds its filter and sort without consuming that field. Indexed searches support order-key continuation, yet the [Query engine](../../../../internal/query/core/engine.go) discards returned order keys and the [HTTP handler](../../../../internal/gateway/rest/handler_query.go) returns only documents. Clients therefore lack a complete, consistently obtainable continuation contract.

The [index design](../../../../docs/design/server/indexer/02.index.md) intends exclusive continuation with deterministic document-ID tie-breaking. Static inspection identifies incomplete integration rather than evidence that every existing indexed page is incorrect.

## Proposal

Define one opaque, versioned public query cursor and a page result containing documents and an optional next cursor. Bind continuation to database, concrete collection, normalized predicates, ordering, deletion visibility, and the applicable index generation. Reject malformed or mismatched cursors explicitly. Use an exclusive last-consumed position; include a unique document-ID tie-breaker even when user sort values are equal.

For direct storage queries, define deterministic default ordering and translate the cursor into a lexicographic Mongo predicate consistent with the requested sort directions. For indexed queries, retain the Indexer's ordering position through materialization and pagination, including skipped missing documents and residual predicates. Both paths must advance based on consumed candidates so a page containing fewer visible documents cannot repeat forever.

Carry the page result through local interfaces, gRPC, HTTP, and SDK types. This changes the current array-only response, so update consumers and API documentation together and explicitly invalidate old cursor formats. The contract provides traversal over changing data, not a historical snapshot; document how mutations of sort keys affect visibility. Emit cursor-format or scope failure categories without logging decoded predicate values.

## Alternatives

**Use only the last document ID.** It is compact and adequate for ID ordering, but cannot continue composite or descending sorts without retrieving mutable source fields.

**Expose raw Indexer order keys.** This reuses existing encoding but leaves direct-storage queries without the same contract and permits accidental reuse after a query or index changes.

## Acceptance Criteria

- Stable datasets traverse to exhaustion without omissions or duplicates for default, ascending, descending, and composite sorts with equal values.
- Both storage and Indexer paths emit usable next cursors through HTTP and the SDK.
- Empty pages after skipped candidates terminate or advance; malformed, cross-database, cross-query, and stale-generation cursors fail explicitly.
- Concurrent sort-key changes have the documented behavior; deadlines cancel underlying scans.

## Risks

Changing response types requires a coordinated client release. Index rebuilds may invalidate active cursors. Maintaining traversal order during asynchronous index materialization needs careful candidate accounting.

## Dependencies

[Indexed predicate semantics](../bug-fix/2026-09-07-indexed-query-filter-semantics.md) owns union and residual filtering; [Indexer recovery](../architecture/2026-09-07-indexer-recovery-lifecycle.md) consumes stable scan pagination.
