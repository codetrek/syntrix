# Agent Note: Reject Unsupported Indexed Query Operators

Status: implemented

## Problem

An indexed query can return extra documents when planning discards a predicate
whose operator has no index translation. SDK query updates and deletes act on
the documents returned by that query, so a widened result can also cause
unintended writes. Shared filter syntax does not establish that every executor
can implement an operator.

## Decision

The Query planner returns an error for an operator it cannot translate. The
indexed query fails before index search and returns no partial result.

| Query shape | Behavior |
|---|---|
| No filters and no ordering | Preserve direct Store execution |
| All filters target `id` with `==` or `in`, without ordering | Preserve direct Store execution |
| Indexed filters using `==`, `>`, `>=`, `<`, or `<=` | Translate using the existing index plan |
| Indexed `!=`, `in`, `contains`, or an unknown operator | Return `model.ErrInvalidQuery` |

The planner error maps to gRPC `InvalidArgument` and HTTP 400 `BAD_REQUEST`.
Its message identifies the unsupported operator and the indexed-query limitation,
without including the field, predicate value, or document payload. Error wrapping
preserves the existing invalid-query identity.
Existing request-validation and missing-Indexer checks retain their precedence
over planning errors. REST request validation rejects unknown operators with
HTTP 400 `BAD_REQUEST` and the generic message `Invalid query parameters`.

The check belongs to indexed Query planning. Shared model and SDK operator types,
write conditions, Store filtering, and realtime filtering retain their existing
contracts. The [Query integration design](../../../../docs/design/server/query/02.indexer-integration.md#filter-planning)
and [filter reference](../../../../docs/reference/filters.md#query-availability)
describe the execution rules.

## Alternatives

**Implement every shared operator in indexed queries.** Membership unions,
disjoint inequality ranges, array index entries, deduplication, and correct
ordering and limits require broader execution and index changes. The existing
[indexed filter proposal](../../proposed/bug-fix/2026-09-07-indexed-query-filter-semantics.md)
retains this work; unsupported indexed queries fail explicitly while it remains
unimplemented.

**Reject the operators in shared validation.** This would also remove valid
ID-only membership queries and affect other consumers of the shared filter
syntax. Executor-specific rejection preserves those contracts.

**Fall back to unrestricted Store scans.** This changes the indexed-query cost
policy and can turn an unsupported request into an unbounded storage operation.
The existing explicit Store routes remain the only exceptions.

## Consequences

- Unsupported indexed filters produce an actionable error instead of a widened
  result. Applications must handle the rejection; the filter reference marks
  affected query examples as unavailable.
- Query-based SDK updates and deletes stop when their initial query rejects.
- The change introduces no index representation, storage format, or public
  operator-type change. Existing supported translations and index selection
  behavior remain subject to their current limitations.
- This check does not establish correct execution for every conjunction of
  otherwise supported operators. Full operator semantics, residual filtering,
  ordering, and pagination remain owned by the linked proposal.
- Adding the missing operators still requires the execution and index work
  recorded there. Keeping the shared operator vocabulary and explicit planner
  error preserves the ability to add a strategy without changing other filter
  consumers or weakening rejection of the remaining unsupported operators.
