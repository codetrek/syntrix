# Query Filters Guide

Syntrix uses shared filter syntax for queries, write conditions, and realtime
subscriptions. Operator availability depends on the operation executing the filter.

## Filter Operators

The shared syntax recognizes these operators:

| Operator | Description | Example |
|----------|-------------|---------|
| `==` | Equality | `{"field": "status", "op": "==", "value": "active"}` |
| `!=` | Not equal | `{"field": "status", "op": "!=", "value": "inactive"}` |
| `>` | Greater than | `{"field": "age", "op": ">", "value": 18}` |
| `>=` | Greater than or equal | `{"field": "age", "op": ">=", "value": 18}` |
| `<` | Less than | `{"field": "score", "op": "<", "value": 100}` |
| `<=` | Less than or equal | `{"field": "score", "op": "<=", "value": 100}` |
| `in` | Value is in a list | `{"field": "role", "op": "in", "value": ["admin", "editor"]}` |
| `contains` | Array contains value | `{"field": "tags", "op": "contains", "value": "news"}` |

## Query Availability

For `POST /api/v1/databases/{database}/query`, execution follows these rules:

| Query shape | Behavior |
|---|---|
| No filters and no `orderBy` | Query Store directly |
| Every filter targets `id` with `==` or `in`, with no `orderBy` | Query Store directly |
| Indexed filters using `==`, `>`, `>=`, `<`, or `<=` | Eligible for index planning; existing index and plan requirements apply |
| Indexed query containing `!=`, `in`, or `contains` | Reject the entire query with HTTP 400 and code `BAD_REQUEST` before index search |
| Unknown operator | Request validation rejects it with HTTP 400 and code `BAD_REQUEST` |

The planner error for `!=`, `in`, or `contains` identifies the unsupported
operator without including its field or value. Unknown operators receive the
generic request-validation message `Invalid query parameters`. No partial result
is returned. In particular, `id in [...]` with `orderBy`
or a non-ID filter uses the indexed path and is rejected. Valid operators alone
do not guarantee that an index can execute every predicate combination.
Indexer configuration and request validation errors retain their existing
precedence over planning errors.

These Query restrictions do not change write-condition, Store, or realtime filter
semantics. Full indexed execution of `!=`, `in`, and `contains` remains
[proposed](../../.agents/notes/proposed/bug-fix/2026-09-07-indexed-query-filter-semantics.md).

### Multiple Conditions on One Indexed Field

On a field usable by the selected index, equality and range conditions are
combined with AND. Their order in `filters` does not change the bounds or result.

| Conditions on `price` | Effective constraint |
|---|---|
| `>= 10` and `>= 20` | `>= 20` |
| `<= 100` and `< 100` | `< 100` |
| `== 20` and `== 20` | `== 20` |
| `== 20` and `== 30` | Empty result |
| `== 20` and `> 20` | Empty result |
| `>= 20` and `<= 20` | Only value 20 |
| `> 20` and `<= 20` | Empty result |

These rules use the index's existing encoded-value comparisons and apply to
ascending and descending fields. Index selection and field-placement requirements
still apply; this does not add residual filtering for fields outside the usable
index prefix or support for additional operators.

## Usage Examples

### Simple Equality
```json
{
  "collection": "users",
  "filters": [
    {"field": "active", "op": "==", "value": true}
  ]
}
```

### Not Equal

Shared syntax example; this indexed Query returns HTTP 400 `BAD_REQUEST`.

```json
{
  "collection": "users",
  "filters": [
    {"field": "status", "op": "!=", "value": "deleted"}
  ]
}
```

### Range Query
```json
{
  "collection": "products",
  "filters": [
    {"field": "price", "op": ">=", "value": 10},
    {"field": "price", "op": "<=", "value": 100}
  ]
}
```

### Array Membership

Shared syntax example; this indexed Query returns HTTP 400 `BAD_REQUEST`.

```json
{
  "collection": "posts",
  "filters": [
    {"field": "tags", "op": "contains", "value": "news"}
  ]
}
```

### In Query

Membership on a non-ID field is unavailable for indexed Query and returns
HTTP 400 `BAD_REQUEST`:

```json
{
  "collection": "users",
  "filters": [
    {"field": "status", "op": "in", "value": ["online", "away"]}
  ]
}
```

An unordered ID-only query remains available through Store:

```json
{
  "collection": "users",
  "filters": [
    {"field": "id", "op": "in", "value": ["alice", "bob"]}
  ]
}
```
