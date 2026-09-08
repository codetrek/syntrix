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
  "condition": "event.document.data.age >= 18",
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
-   **`events`**: List of event types to listen for: `create`, `update`, `delete`. Here `delete` means Syntrix logical deletion; Mongo physical removal does not fire a trigger.
-   **Deletion data**: A tombstone retains metadata and clears business fields. Access to former values requires the proposed [before-image capability](../../.agents/notes/proposed/feature/2026-09-07-trigger-before-images.md).
-   **`condition`**: A CEL expression string. If this evaluates to `true`, the webhook is fired. If empty, it defaults to `true`.
-   **`url`**: The destination URL for the webhook POST request.
-   **`retryPolicy`**: Configuration for retrying failed deliveries. Backoff times are duration strings (e.g., `1s`, `100ms`, `1m`).

## Writing Conditions (CEL)

Syntrix uses Google's [Common Expression Language (CEL)](https://github.com/google/cel-spec) for defining trigger conditions. CEL is a simple, safe, and fast expression language.

### The `event` Context

All conditions are evaluated against an `event` variable. The structure of `event` is:

```json
{
  "type": "create",          // "create", "update", or "delete"
  "path": "users/user_123",  // Full document path
  "timestamp": 1697041234,   // Event timestamp (Unix)
  "document": {              // The document state (User-Facing Document)
    "id": "user_123",        // Business ID
    "collection": "users",
    "version": 1,
    "name": "Alice",         // Flattened fields
    "age": 25,
    "role": "admin",
    "tags": ["vip", "beta"]
  }
}
```

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
event.type == 'update' && event.document.data.status == 'shipped'
```

## Testing Rules

You can test your CEL expressions using the [CEL Playground](https://playcel.undistro.io/) (select "Generic" environment) or by writing unit tests in your application code.

## Limitations

-   **Deterministic**: CEL functions must be deterministic. Randomness (e.g., `rand()`) or time-based functions (e.g., `now()`) are generally not supported inside the condition to ensure replayability.
-   **No External Calls**: Conditions cannot make network requests or database lookups. They can only evaluate the data present in the `event` object.
