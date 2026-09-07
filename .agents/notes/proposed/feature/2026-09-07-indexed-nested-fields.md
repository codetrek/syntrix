# Agent Note: Resolve Nested Field Paths Consistently in Indexes

Status: proposed

## Problem

The [Indexer field extractor](../../../../internal/indexer/service.go) claims support for paths such as `user.name`, but its implementation is `return data[field]`. A document shaped as `{"user":{"name":"Ada"}}` therefore does not yield `"Ada"` for that indexed path. The template can appear usable while its keys represent a missing value. This incomplete capability is established by source inspection.

The [index design](../../../../docs/design/server/indexer/02.index.md) derives ordered keys from template fields. Live events and future rebuilds must interpret those fields identically or a rebuild will change query results.

## Proposal

Specify dot-separated object paths for index template fields and query fields, with a shared resolver used by live key construction and rebuild key construction. A path walks object members one segment at a time. Define missing members, explicit null, and a non-object intermediate value consistently with storage predicate semantics. Validate malformed paths, including empty segments, when loading templates and planning queries.

Do not infer array traversal from object-path syntax; array membership remains the separately specified operator capability. Define how literal dotted property names are represented or declared inaccessible through this path syntax before accepting such templates, avoiding an ambiguous fallback from nested lookup to a top-level dotted key.

Reuse existing scalar order encoding after value resolution. Mark indexes affected by the extractor change as requiring reconstruction and invalidate their old cursors, since existing persisted keys may encode missing values. Cover equality, ranges, ordering, updates that move a nested value, and removal of a nested member. Errors should identify the template and field path without serializing source documents.

## Alternatives

**Flatten documents before storage.** This simplifies lookup but changes stored user-data shape and creates collisions between literal dotted names and nested objects. Field interpretation belongs at the indexing boundary.

**Restrict indexes to top-level fields.** This makes the current implementation honest and avoids extraction rules, but withdraws the stated nested-field capability. It remains viable only if that requirement is explicitly changed.

## Acceptance Criteria

- The nested-object example indexes and queries `user.name` as `"Ada"` in memory and persistent modes.
- Missing, null, non-object intermediate, literal dotted names, and malformed paths each have specified and tested outcomes.
- Live ingestion and rebuild produce identical keys; nested updates and deletes remove obsolete entries.
- Cross-database fixtures remain isolated, and an affected persisted index cannot become ready without reconstruction.

## Risks

Resolving previously missing values changes ordering and index membership. Inconsistent path rules between Mongo, Indexer, and query validation would recreate deployment-dependent results. Path parsing should avoid repeated allocation on the event hot path.

## Dependencies

[Indexer recovery](../architecture/2026-09-07-indexer-recovery-lifecycle.md) owns reconstruction, and [query cursor pagination](2026-09-07-query-cursor-pagination.md) owns invalidation of old continuation formats and generations.
