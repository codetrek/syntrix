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
  "timeout": "30s",
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
-   **`timeout`**: Per-attempt execution budget as a duration string (e.g., `"30s"` or `"2m"`); see [delivery timeouts](#delivery-timeouts).
-   **`retryPolicy`**: Configuration for [delivery retries](#delivery-retries). Backoff times are duration strings (e.g., `1s`, `100ms`, `1m`).

## Delivery Timeouts

| Rule `timeout` | Behavior |
|----------------|----------|
| Omitted or zero | Capture a 30-second timeout in each new task |
| Positive duration | Capture that configured timeout unchanged |
| Negative duration | Reject the rule set and preserve the previously active rules |

Malformed or unrepresentable duration strings fail configuration parsing. There is no
additional timeout maximum. A task retains its captured value across retries;
later rule edits apply to newly created tasks.

The execution budget starts after queue waiting and covers preparation, secret
resolution, system-token signing, and HTTP work together. Every retry receives
a fresh budget; waiting for delayed redelivery does not consume it. Earlier
parent cancellation or deadlines still apply. Timeout failures follow the
existing retry policy and attempt limit.

The HTTP worker adds no total client timeout by default. A caller that explicitly
sets a positive HTTP client timeout adds that cap; direct worker callers must
provide their own bounded or cancellable context. Context-aware operations observe
cancellation. Local RSA token signing is synchronous and cannot be preempted by
its context; it consumes elapsed budget but may complete after the deadline.
Cancellation does not undo effects already performed by a receiver.

The broker acknowledgement window includes both local queue waiting and execution.
It is independent of the rule budget, so long waits or attempts can overlap broker
redelivery. The proposed [durable task handoff](../../.agents/notes/proposed/architecture/2026-09-07-trigger-acknowledgement-window.md)
would acknowledge after durable task acceptance and recover execution from shared
database state; it is not implemented. The [timeout decision](../../.agents/notes/implemented/bug-fix/2026-09-07-trigger-rule-timeouts.md)
records the delivered ownership and limits.

## Delivery Retries

HTTP 2xx completes delivery. HTTP 429 is retried because endpoint throttling can
recover after waiting, subject to the hint limit below. Other 4xx responses
terminate immediately. Other non-2xx responses returned by the HTTP client,
network errors, and attempt timeouts follow the retry policy. Redirect handling
remains the HTTP client's existing behavior.

`maxAttempts` counts total deliveries, including the initial attempt. A task
value of zero uses 3 attempts. When a retryable failure reaches that limit, the
consumer logs exhaustion and terminates the message. After failed delivery number
`n`, rule backoff is `initialBackoff * 2^(n-1)`; a zero task `initialBackoff` uses
1 second. A positive `maxBackoff` caps that component. For example, 3 attempts
with an initial backoff of 1 second and a maximum of 10 seconds schedule retries
after 1 and 2 seconds when no usable hint is present.

For HTTP 429, the final delay is `max(capped rule backoff, Retry-After hint)`.
A response hint is a minimum pause and can exceed `maxBackoff`; it does not add
attempts or change retry defaults.

| `Retry-After` on HTTP 429 | Behavior |
|-------------------------|----------|
| One positive decimal delay-seconds value | Honor that delay when longer than rule backoff |
| One supported future HTTP-date | Honor its relative delay measured when the response is parsed |
| Missing, empty, malformed, signed, negative, zero, past/equal date, or multiple physical fields | Use rule backoff |
| Valid delay beyond the supported duration range | Terminate with `Retry-After exceeds the maximum supported delay` |

Surrounding space or tab is ignored. Malformed combined values are ignored;
HTTP-date parsing accepts the standard library's supported formats. Hints on
statuses other than 429 do not affect scheduling. There is no additional business
delay ceiling: the technical limit is a signed 64-bit nanosecond duration,
approximately 292 years. Long representable hints can retain delayed work for
long periods; clock skew affects date-based pauses. Unrepresentable valid hints
are terminal rather than shortened into earlier retries. Diagnostics omit raw
hint values and response bodies.

Retries use queue-delayed redelivery without occupying a worker during the wait.
Scheduling failures are logged, and recovery retains the selected queue backend's
existing limits. The in-memory queue does not preserve delayed work across
shutdown or restart. Retried requests can repeat external side effects; receivers
must account for duplicates. The
[Retry-After decision](../../.agents/notes/implemented/feature/2026-09-07-trigger-retry-after.md)
records the policy and tradeoffs.

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
