# Agent Note: Honor Retry-After for Trigger Deliveries

Status: proposed

## Problem

[Rate-limited deliveries retry](../../implemented/bug-fix/2026-09-07-trigger-retryable-http-statuses.md)
using the rule's backoff. A 429 response's `Retry-After` hint does not affect the
schedule, so retries can arrive during the endpoint's requested pause and amplify
overload. Status classification alone does not define how response hints interact
with the rule's delay and attempt limits.

## Proposal

For 429 responses, carry an optional parsed `Retry-After` delay from the worker
to the consumer while retaining the response status and error chain. Support
delay-seconds and HTTP-date forms using a controllable clock. Absent or invalid
hints use rule backoff. Define a bounded next-attempt delay from the hint and rule
backoff while preserving the task's maximum attempts.

The consumer remains the sole owner of retry accounting and scheduling, so queue
delivery metadata, task limits, and terminal outcomes agree. Schedule retries
through the queue without sleeping while holding a worker slot. Record status
category, attempt number, scheduled delay, and stable task identity without
recording response bodies or request payloads. Stable identity remains owned by
the [delivery idempotency proposal](../architecture/2026-09-07-trigger-delivery-idempotency.md);
this proposal does not establish that identity or durable redrive by itself.

Before implementation, decide the hint/backoff combination rule, treatment of
past dates and excessive hints, and maximum delay. The existing rule caps backoff
only when `maxBackoff` is positive; it supplies no universal hard delay limit.
Document whether a hint exceeding the chosen bound is capped, rejected, or given
another explicit outcome, and how HTTP-date clock skew affects the result.
The scope is 429; support for hints on other statuses requires a separate decision.

Deferral leaves observable rule-only scheduling in place. Adding this feature
requires header parsing, a worker-to-consumer hint representation, and consumer
scheduling changes. Keeping classification in the worker and scheduling in the
consumer preserves that extension point without changing the delivered retry
budget or introducing worker-local timers.

## Alternatives

**Use rule backoff only** fixes the lost-retry bug and remains the current public
contract, but may continue sending requests during an endpoint's requested pause.
Reconsider it when endpoint throttling behavior or operator requirements justify
honoring hints and the bounded scheduling rules are specified.

**Deliver hint support together with the 429 classification repair** would remove
that operational gap immediately, but would tie the existing retry contract to
new parsing and scheduling decisions. The classification repair is independently
implemented; the requirements for honoring hints remain here.

## Acceptance Criteria

- A scripted endpoint returning 429 then 2xx causes bounded redelivery and one
  successful acknowledgement; ordinary terminal 4xx responses are not retried.
- 5xx, network errors, and attempt timeouts retain their retry behavior and task
  attempt limits remain unchanged.
- Delay-seconds and HTTP-date hints, absent or invalid values, past dates, and
  excessive values produce the exact documented bounded schedule under a
  controlled clock. The response hint and rule backoff interaction is explicit.
- Attempt exhaustion terminates with an identifiable outcome. A scheduled delay
  retains no active worker; shutdown leaves delayed tasks recoverable within the
  selected queue backend's documented durability limits. This must not imply
  process-restart recovery for the in-memory queue.
- Diagnostics expose the chosen schedule and attempt without response bodies or
  request payloads, and preserve stable identity when that capability is present.

## Risks

Large hints can defer delivery for a long time; capping them may retry during the
endpoint's requested pause. Retry amplification can worsen overload. HTTP-date
interpretation depends on clock skew and the defined past-date behavior.

Retried requests can repeat side effects. Receivers need the stable identity and
cooperative deduplication described by delivery idempotency; hint support cannot
promise exactly-once effects. Recoverability remains bounded by the queue backend
and does not replace durable delivery storage.
