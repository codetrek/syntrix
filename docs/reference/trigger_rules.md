# Trigger Rules Guide

Configure triggers with JSON and write conditions using
[Common Expression Language (CEL)](https://github.com/google/cel-spec).

## Trigger Configuration

```json
{
  "triggerId": "user-signup-welcome",
  "version": "1.0",
  "database": "acme",
  "collection": "chats/*/members",
  "events": ["create"],
  "condition": "event.document.age >= 18",
  "url": "http://localhost:3000/webhooks/welcome",
  "headers": {
    "X-Custom-Header": "value"
  },
  "retryPolicy": {
    "maxAttempts": 3,
    "initialBackoff": "1s",
    "maxBackoff": "10s"
  }
}
```

| Field | Meaning |
|---|---|
| `triggerId` | Unique trigger identifier |
| `collection` | Logical collection pattern; `chats/*/messages` matches `chats/room1/messages` |
| `events` | Business event types: `create`, `update`, `delete` |
| `condition` | Boolean CEL expression; an empty condition matches after scope filtering |
| `url` | Webhook POST destination |
| `retryPolicy` | Delivery retry settings; backoffs use duration strings such as `1s`, `100ms`, or `1m` |

`delete` means Syntrix logical document deletion. MongoDB physical document
removal does not fire a trigger.

## CEL Event Context

Conditions receive one `event` object:

```json
{
  "type": "create",
  "timestamp": 1697041234000,
  "document": {
    "id": "acme:<document-hash>",
    "collection": "users",
    "version": 1,
    "name": "Alice",
    "age": 25,
    "role": "admin",
    "tags": ["vip", "beta"]
  },
  "before": null
}
```

| Field | Meaning |
|---|---|
| `type` | Business event type |
| `timestamp` | Puller normalization time in Unix milliseconds |
| `document` | Flattened business data plus stored `id`, `collection`, and `version`; metadata overwrites colliding business fields |
| `document.id` | Storage identifier; original business-ID access is not guaranteed |
| `before` | Previous image; currently `null` for all production event types |

There is no `event.path`, nested `event.document.data`, or storage `deleted` flag
in the CEL context.

## Logical Deletion and Previous Data

The [storage deletion lifecycle](../design/server/core/storage/03.stores.md#document-deletion-and-physical-cleanup)
retains a tombstone with `deleted=true`, business `data={}`, and document metadata.

| Operation | Trigger event | Available business data |
|---|---|---|
| Logical deletion | `delete`; `event.document` retains `id`, `collection`, and `version` | Cleared; previous fields are unavailable |
| Physical document removal, including later tombstone cleanup | None | No additional deletion trigger |

Logical-delete CEL input:

```json
{
  "type": "delete",
  "timestamp": 1697041235000,
  "document": {
    "id": "acme:<document-hash>",
    "collection": "users",
    "version": 2
  },
  "before": null
}
```

| Capability | Availability |
|---|---|
| Match a logical deletion | Use `event.type == 'delete'` and retained metadata |
| Read previous values or compare old and new values | Requires proposed [before-image capture](../../.agents/notes/proposed/feature/2026-09-07-trigger-before-images.md) |
| Enable previous images with `includeBefore` | Current production input does not capture them; the setting cannot supply absent data |
| Access cleared or optional fields | Use `has(event.document.field)` for optional data; guards cannot recover previous values |
| Webhook business payload for deletion | Empty; JSON omits `payload`. Event type and routing metadata remain present |

## Matching and Errors

| Outcome | Current behavior |
|---|---|
| Scope mismatch or CEL `false` | No task |
| Scope match with CEL `true` or an empty condition | Create a delivery task |
| Invalid CEL, non-boolean result, missing-field access, or null-image dereference | Evaluation error; service logs the failure and skips that rule for the event |

The current error handling does not guarantee retry or recovery of a failed
evaluation. Historical-image failure handling remains part of the before-image
proposal.

## Condition Examples

| Intent | CEL condition |
|---|---|
| Adult user | `event.document.age >= 18` |
| Administrator | `event.document.role == 'admin'` |
| Active VIP | `event.document.isActive == true && event.document.isVIP == true` |
| Beta participant | `'beta' in event.document.tags` |
| Email exists and is non-null | `has(event.document.email) && event.document.email != null` |
| High-value or selected-region order | `event.document.amount > 1000 \|\| event.document.region in ['US', 'EU']` |
| Updated document is shipped | `event.type == 'update' && event.document.status == 'shipped'` |
| Logical deletion | `event.type == 'delete'` |

The shipped-status example checks the current state; it does not prove that
`status` changed.

## Testing and Limits

- Test expressions in the [CEL Playground](https://playcel.undistro.io/) with a
  Generic environment and representative event data, or in application tests.
- Conditions must be deterministic for replayability; random and current-time
  functions are unsupported.
- Conditions cannot make network requests or database lookups. They evaluate
  only the supplied event data.
