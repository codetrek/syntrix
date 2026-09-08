# Trigger Rules Guide

This guide explains how to configure Triggers in Syntrix and how to write conditions using the Common Expression Language (CEL).

## Trigger Configuration

A Trigger is defined by a JSON configuration object. Here is the structure:

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

### Fields

-   **`triggerId`**: Unique identifier for the trigger.
-   **`collection`**: The database collection to watch (e.g., `users`, `orders`). Supports wildcards (e.g., `chats/*/messages` matches `chats/room1/messages`).
-   **`events`**: List of business event types to listen for: `create`, `update`, `delete`. `delete` means Syntrix logical document deletion; MongoDB physical document deletion does not fire a trigger.
-   **`condition`**: A CEL expression string. If this evaluates to `true`, the webhook is fired. If empty, it defaults to `true`.
-   **`url`**: The destination URL for the webhook POST request.
-   **`retryPolicy`**: Configuration for retrying failed deliveries. Backoff times are duration strings (e.g., `1s`, `100ms`, `1m`).

## Writing Conditions (CEL)

Syntrix uses Google's [Common Expression Language (CEL)](https://github.com/google/cel-spec) for defining trigger conditions. CEL is a simple, safe, and fast expression language.

### The `event` Context

All conditions are evaluated against an `event` object. For a create event, its
structure is:

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

`timestamp` is the Puller normalization time in Unix milliseconds. `document`
contains flattened business data plus the stored document `id`, `collection`,
and `version`. These metadata fields overwrite business fields with the same
names. The stored `id` is not the original business ID; conditions should not
assume that the latter is available in this context. The context has no `path`
field or nested `document.data` object.

### Logical Deletion and Previous Data

Syntrix logical deletion updates the MongoDB document to retain a tombstone with
`deleted=true`, empty business `data`, and document metadata. Trigger evaluation
receives a business `delete` event whose `document` contains only `id`,
`collection`, and `version`:

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

Use `event.type == 'delete'` to identify logical deletion. The previous business
fields are absent, and the CEL document does not expose the stored `deleted`
flag. MongoDB physical document deletion, including later tombstone cleanup,
does not produce a business trigger event. See the [storage deletion contract](../design/server/core/storage/03.stores.md#document-deletion-and-physical-cleanup).

The production Puller path does not capture previous document images, so
`event.before` is `null` for all event types. `includeBefore` does not enable
capture. Conditions that compare old and new field values, or inspect a balance
before deletion, require the proposed [before-image capability](../../.agents/notes/proposed/feature/2026-09-07-trigger-before-images.md).

Missing business fields and access through a null `before` value produce an
evaluation error, which is logged and skips that trigger for the event. Use
`has(event.document.field)` when a field is optional. Guarding absent data can
prevent an error, but cannot recover its previous value.

The CEL context is separate from the webhook delivery task. For logical
deletion, the current task builder uses the tombstone's empty business map as
`Payload`, which JSON serialization omits. Delivery still carries the `delete`
event type and routing metadata; it does not carry the deleted business data.

### Examples

#### 1. Simple Field Check
Trigger only when a user's age is 18 or older.
```cel
event.document.age >= 18
```

#### 2. String Matching
Trigger when a user's role is 'admin'.
```cel
event.document.role == 'admin'
```

#### 3. Boolean Logic
Trigger for active users who are also VIPs.
```cel
event.document.isActive == true && event.document.isVIP == true
```

#### 4. List Operations
Trigger if the user has the 'beta' tag.
```cel
'beta' in event.document.tags
```

#### 5. Null Checks
Trigger if the 'email' field exists and is not null.
```cel
has(event.document.email) && event.document.email != null
```

#### 6. Complex Logic
Trigger on high-value orders (amount > 1000) OR orders from specific regions.
```cel
event.document.amount > 1000 || event.document.region in ['US', 'EU']

```

#### 7. Event Type Specific
Although you usually filter by `events` array in config, you can also check in CEL:
```cel
event.type == 'update' && event.document.status == 'shipped'
```

#### 8. Logical Deletion
Trigger when a document in the configured collection is logically deleted:
```cel
event.type == 'delete'
```

## Testing Rules

You can test your CEL expressions using the [CEL Playground](https://playcel.undistro.io/) (select "Generic" environment) or by writing unit tests in your application code.

## Limitations

-   **Deterministic**: CEL functions must be deterministic. Randomness (e.g., `rand()`) or time-based functions (e.g., `now()`) are generally not supported inside the condition to ensure replayability.
-   **No External Calls**: Conditions cannot make network requests or database lookups. They can only evaluate the data present in the `event` object.
-   **No Previous Images**: Current production events do not provide `event.before`. Deletion conditions can use the event type and retained metadata, but cannot read cleared business fields.
