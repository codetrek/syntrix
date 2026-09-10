# Agent Note: Expose Actual Indexer Statistics over gRPC

Status: implemented

## Problem

The remote Indexer client returned successful zero-valued statistics without
contacting the service. Callers could not distinguish an idle service from
unavailable telemetry, although the local service already owned template count,
event count, and last-event time. The existing Stats interface requires the same
field meanings in standalone and distributed deployments.

## Decision

The [Stats RPC contract](../../../../docs/design/server/indexer/03.grpc.md#statistics)
uses an empty request and three `int64` response fields. The gRPC adapter calls
`Service.Stats(ctx)`, which combines the Manager's template count with the
Service's event statistics. The client performs a real RPC using the caller's
context and maps the response to the existing Go Stats type.

| Field | Meaning |
|---|---|
| `template_count` | Number of currently loaded templates, across all databases; not the number of index instances |
| `events_applied` | Events that complete the matching-template processing loop, counted once per event even when individual template writes log errors |
| `last_event_time` | Local Unix seconds recorded after the latest event count update; zero when no event has been counted |

Events that return before the matching-template loop do not count. The request
has no database selector. A new Service instance starts its event count and time
at zero; reloading templates preserves both. These values do not establish
successful indexing, a source event timestamp, checkpoint, or replication lag.

Statistics sample existing memory: a short Manager read lock and separate atomic
loads. No storage scan or index catch-up is required. Concurrent reads are safe,
but the fields are not one atomic snapshot. The local service checks context
before and after sampling; cancellation cannot interrupt a mutex wait.

| Failure | Behavior |
|---|---|
| Canceled or expired context | Return `Canceled` or `DeadlineExceeded` |
| Existing gRPC status from the service | Preserve its status code |
| Other collection error | Return `Internal` |
| Unreachable server | Return the transport error, including `Unavailable` |
| Server without the RPC | Return `Unimplemented` |
| Negative or platform-unrepresentable template count | Reject the response before conversion to Go `int` |

The client wraps failures with the operation and endpoint while retaining the
error chain. It does not replace failures with successful zero values. Event
count and time remain signed 64-bit integers throughout transport.

## Alternatives

**Derive statistics from Health/GetState.** These operations expose health and
index state, but cannot reconstruct the Service's event count or processing
time faithfully.

**Use only a metrics exporter.** Exported metrics support operational
aggregation, but do not satisfy the existing Stats interface. Exporter decisions
remain owned by [application observability](../../proposed/architecture/2026-09-07-application-observability.md).

## Consequences

- Local and remote calls observe the same fields, units, and instance scope.
  Equal values are expected for a quiescent service; concurrent samples may differ.
- A successful all-zero result is valid for an empty new instance. It does not
  indicate whether an instance restarted; consumers must account for counter resets.
- Reading statistics adds no work to the event-processing path and performs no
  document or index scans.
- The additive RPC requires no stored-data migration. A client connecting to a
  server without Stats receives an explicit unsupported-operation error.
- Lag, hit rate, rebuild duration, and per-database metrics require their own
  measurements; the three existing fields cannot be used to infer them.
