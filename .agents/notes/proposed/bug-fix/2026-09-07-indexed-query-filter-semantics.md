# Agent Note: Preserve Every Accepted Predicate in Indexed Queries

Status: proposed

## Problem

The shared [model operators](../../../../pkg/model/filter.go) include `!=`, `in`,
and `contains`, but indexed Query has no execution strategy for them. The original
source inspection found that planning discarded these predicates, potentially
widening results or selecting a different index. The
[unsupported-operator rejection decision](../../implemented/bug-fix/2026-09-11-query-unsupported-filter-rejection.md)
prevents that omission by rejecting indexed queries before search. Unordered
ID-only `==` and `in` queries retain their direct Store path.

The [filter reference](../../../../docs/reference/filters.md#query-availability)
records these execution limits. General membership, not-equal, and array
membership queries remain unavailable through indexed Query; complete operator
semantics and predicate-combination correctness remain the subject of this
proposal.

## Proposal

Retain the planner's explicit unsupported-operator error until each accepted
predicate has an execution strategy. Implement the shared operator set through
index-aware plans: union equality ranges for `in`, disjoint ranges for `!=`, and
array-membership indexing for `contains`. Specify missing-field, null, numeric,
and array semantics consistently with storage filtering. Composite predicates
must retain conjunction semantics.

Merge and deduplicate candidate streams before applying the requested order and limit. When predicates require residual evaluation against fetched documents, continue candidate traversal until enough matching documents are collected or the index is exhausted; never treat an arbitrary candidate limit as the final result limit. Preserve explicit no-matching-index and unsupported-plan failures when no valid strategy exists.

Update Indexer protocol operators, template validation, ordering/cursor handling, and reference examples as one contract change. Array membership may require multiple entries per document and a versioned index representation; rebuild affected derived indexes through the recovery lifecycle. Report the failed operator and plan reason without logging predicate values or document payloads.

## Alternatives

**Reject the three operators everywhere.** This removes behavior promised by the
shared filter contract, including ID-only membership and other filter consumers.
The implemented rejection is limited to indexed Query and does not withdraw the
full indexed-execution requirement retained here.

**Run unsupported queries as unrestricted storage scans.** Storage can evaluate more predicates, but this changes the required-index and predictable-cost policy in the [Query integration design](../../../../docs/design/server/query/02.indexer-integration.md).

## Acceptance Criteria

- Each documented operator and mixed conjunction returns the same IDs as a defined reference evaluation over fixtures with nulls, missing fields, arrays, and duplicate values.
- Limits and multi-page ordering remain correct across union branches, duplicate candidates, and residual filtering.
- Local and gRPC Indexer paths agree, including explicit unsupported-plan errors and cancellation.
- Queries never cross database or collection scope, and a predicate can never be silently removed.

## Risks

Large membership sets and low-selectivity residual predicates increase work; explicit limits and cancellation are required. Array index expansion increases write/storage cost. Eventual index consistency remains unchanged.

## Dependencies

[Query cursor pagination](../feature/2026-09-07-query-cursor-pagination.md) owns stable continuation, and [Indexer recovery](../architecture/2026-09-07-indexer-recovery-lifecycle.md) owns derived-index rebuilds.
