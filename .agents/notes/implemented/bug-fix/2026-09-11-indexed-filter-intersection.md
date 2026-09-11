# Agent Note: Intersect Same-Field Indexed Filters

Status: implemented

## Problem

Multiple conditions on one indexed field can lose their AND semantics when
bound construction selects one equality, replaces a stronger range with a weaker
one, or ignores ranges after finding equality. Reordering equivalent predicates
can then change the result. Inconsistent equalities or disjoint bounds can also
return documents even though no value satisfies the query.

## Decision

Indexer intersects supported predicates on each field usable by the selected
index, using the existing encoded-value comparison semantics.

| Constraint | Rule |
|---|---|
| Repeated `==` | Encoded values must agree |
| `==` with ranges | The equality value must satisfy all ranges |
| Multiple `>` or `>=` | Keep the strongest lower bound; strict wins an equal-value tie |
| Multiple `<` or `<=` | Keep the strongest upper bound; strict wins an equal-value tie |
| Contradictory constraints | Return an empty result |
| Equal lower and upper values | Match that value only when both bounds are inclusive |

Predicate order does not change the intersection. Ascending and descending index
fields apply the same logical constraints; encoded key bounds preserve whether
all keys with the boundary value are included or excluded. Equality prefixes
continue into subsequent usable index fields. Index selection and cursor decoding
precede bound construction. A contradiction returns an empty result before
backend index search; backend readiness errors are not observed on that path.
Encoding errors on visited usable fields still propagate. Field placement,
limits, cursor protocol, and backend search for nonempty bounds remain unchanged.

The [index design](../../../../docs/design/server/indexer/02.index.md#same-field-constraints)
owns the bound rules; the [filter reference](../../../../docs/reference/filters.md#multiple-conditions-on-one-indexed-field)
owns caller examples. [Unsupported-operator rejection](2026-09-11-query-unsupported-filter-rejection.md)
continues to reject indexed `!=`, `in`, and `contains`.

## Alternatives

**Fix only overwritten range bounds.** This leaves repeated equality and
equality/range contradictions able to broaden the same field's constraints.
All supported predicates on that field need one intersection rule.

**Complete all indexed predicate semantics together.** Membership unions, array
index entries, cross-field residual evaluation, and cursor correctness require
broader execution and index changes. The existing
[indexed filter proposal](../../proposed/bug-fix/2026-09-07-indexed-query-filter-semantics.md)
owns those requirements and their costs.

## Consequences

- Repeated equality and range conditions on usable fields retain AND semantics
  regardless of predicate order, including strict endpoint ties and descending
  keys. Contradictions produce an empty result after index selection and cursor
  decoding, without searching the backend index.
- No storage or index representation change is required. Number encoding and
  existing comparisons involving missing or null values are not redefined.
- Fields outside the usable index prefix do not acquire residual filtering.
  This decision does not establish correct execution of every cross-field
  conjunction or add indexed `!=`, `in`, or `contains`.
- Full predicate execution remains deferred to the linked proposal. Its union,
  residual traversal, ordering, and index-representation work is still required;
  the same-field intersection rule remains applicable when those strategies are
  introduced.
