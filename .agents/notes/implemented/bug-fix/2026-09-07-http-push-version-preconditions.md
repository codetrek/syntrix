# Agent Note: Preserve HTTP Push Version Preconditions

Status: implemented

## Problem

The HTTP Push handler stripped `document.version` before extracting it and passed
no `BaseVersion` to Query. The documented optional version precondition was lost,
so existing live-target version checks could not protect HTTP writes. Decoding
the value only through the generic document map would also round integers beyond
JSON's commonly used floating-point precision. Read/write splitting could also
feed Push a stale replica version or apparent absence before a write, even when
the HTTP precondition was preserved.

## Decision

The local [HTTP change decoder](../../../../internal/gateway/rest/types.go)
extracts the exact, case-sensitive `document.version` field from its raw JSON
before decoding ordinary document data. It stores the precondition in an internal
`BaseVersion *int64` field excluded from JSON output.

| Supplied version | Internal result |
|---|---|
| Omitted | `nil`; preserve the optional unconditional-write behavior |
| Nonnegative int64 integer literal, including explicit zero | Preserve the exact value and presence |
| Null, string, boolean, negative value, fraction, exponent notation, or out-of-range integer | Reject the request with HTTP 400 before any Engine call |

Decoding retains ordinary business numbers as `float64` and leaves generic
`model.Document` behavior unchanged. A reused change value is replaced only after
successful decoding, so a later valid change without a version clears any prior
precondition. A malformed version anywhere in a batch prevents the entire request
from reaching the Engine; this input validation does not make writes transactional.

[The handler](../../../../internal/gateway/rest/handler_replication.go) forwards
the extracted precondition separately while continuing to strip protected fields.
`NewStoredDoc` retains its version-1 initialization; client versions are not copied
into stored metadata. Storage owns resulting versions.

The change reuses Query's existing live-target version comparison and atomic
write predicates. Omitted versions remain absent across gRPC through the existing
`-1` encoding; zero and all supported positive int64 values retain their values.
No protobuf, SDK, action, or conflict-response contract changes are introduced.
In particular, `create` with version 1 remains accepted, and zero is an equality
precondition on an existing live target, not an insert-only instruction.

Push's initial lookup and all three conflict lookups now explicitly request
`ReadOptions{Consistency: ReadAuthoritative}`. The routed store selects that
logical database's `OpWrite` source and forwards the option. Mongo clones the
collection handle for this call with primary read preference, preserving shared
client/collection settings. Ordinary `Get`, `GetMany`, and `Query` retain their
configured read behavior.

`Get` accepts zero or one options value. Omission or `ReadDefault` uses ordinary
read routing; unsupported modes and multiple values fail. Authoritative-source
selection and read errors propagate without a replica fallback. Push also
propagates non-`ErrNotFound` conflict-read errors instead of returning an
incomplete success response. Genuine absence still follows the existing paths.

Authoritative selection requires a writer-capable backend topology. A Mongo
connection explicitly pinned to a secondary is not made writer-capable by setting
primary read preference. The option selects the write source; it does not lock a
document, create a transaction, or establish linearizable reads. Atomic write
predicates remain necessary because data can change after the read.

## Alternatives

**Add a separate public `baseVersion` field** would distinguish payload from
metadata explicitly, but changes the established flattened protocol when
`document.version` already owns the optional precondition.

**Decode all document numbers as exact-number wrappers** would preserve version
precision, but also changes business-data runtime types throughout the existing
document path. A local raw-field decoder preserves precision without that change.

**Add a separate `GetForWrite` method** would name the intent explicitly but
duplicate the single-document read API. A typed per-call option keeps the routing
requirement visible and forwards it through the existing interface.

**Carry consistency in context values** would avoid an explicit parameter but
hide a correctness requirement from the storage call. `ReadOptions` exposes the
request and supports validation without changing ordinary read routing.

**Implement strict create/update/delete semantics with this repair** would also
address absent and tombstoned targets, but requires Query/storage predicates and
a coordinated conflict/protocol migration. Those guarantees remain owned by the
[original proposal](../../proposed/bug-fix/2026-09-07-replication-push-version-checks.md).

## Consequences

HTTP writes now reach existing live-target checks with their exact optional
precondition. A stale live version conflicts; a matching version can proceed
subject to the write predicate and other storage outcomes. The original
[replication reference](../../../../docs/reference/replication.md#version-preconditions)
now describes the accepted input and current limits.

This repair does not establish complete conditional-write safety. Query still
takes its not-found/Create branch before checking the precondition, so a missing
or deleted target can be recreated. Concurrent deletion can still leave an
incomplete conflict result when the authoritative lookup returns `ErrNotFound`.
Other conflict-read errors now propagate. The original proposal retains these
bugs, strict action/version combinations, tombstone-aware reads, structured
conflicts, and protocol migration. Retries after a lost response remain ambiguous;
version checks do not establish exactly-once effects.
