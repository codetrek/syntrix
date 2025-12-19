# Query Filters Guide

Syntrix supports a specific set of operators for filtering documents in queries and realtime subscriptions.

## Supported Operators

The following operators are supported:

| Operator | Description | Example |
|----------|-------------|---------|
| `==` | Equality | `{"field": "status", "op": "==", "value": "active"}` |
| `>` | Greater than | `{"field": "age", "op": ">", "value": 18}` |
| `>=` | Greater than or equal | `{"field": "age", "op": ">=", "value": 18}` |
| `<` | Less than | `{"field": "score", "op": "<", "value": 100}` |
| `<=` | Less than or equal | `{"field": "score", "op": "<=", "value": 100}` |
| `in` | Value is in a list | `{"field": "role", "op": "in", "value": ["admin", "editor"]}` |
| `array-contains` | Array field contains value | `{"field": "tags", "op": "array-contains", "value": "golang"}` |

## Unsupported Operators

*   `!=` (Not Equal): This operator is not supported because it is inefficient for indexing. Use a combination of `<` and `>` or restructure your data.

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
```json
{
  "collection": "posts",
  "filters": [
    {"field": "tags", "op": "array-contains", "value": "news"}
  ]
}
```

### In Query
```json
{
  "collection": "users",
  "filters": [
    {"field": "status", "op": "in", "value": ["online", "away"]}
  ]
}
```
