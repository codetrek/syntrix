# Agent Note: Expose Actual Indexer Statistics over gRPC

Status: proposed

## Problem

The local [Indexer service](../../../../internal/indexer/service.go) reports template count, events applied, and last-event time. The remote [client](../../../../internal/indexer/client/client.go) implements the same Stats method with `return manager.Stats{}, nil`, while the [protocol](../../../../api/proto/indexer.proto) has no statistics RPC. Distributed callers therefore receive successful zero-valued data without contacting the service. This is a confirmed adapter gap from static inspection.

The [Indexer requirements](../../../../docs/design/server/indexer/00.requirements.md) include health and statistics. Meaningful zero activity and unavailable telemetry must remain distinguishable.

## Proposal

Add a statistics RPC and map it through the existing local service, server adapter, and remote client. Initially expose the actual fields already owned by the service with identical units, scope, and meaning in both deployment modes. Define counters as process-lifetime values, template count as the current configured count, and last-event time as the service's most recent applied-event timestamp, including the explicit never-observed state.

Return transport, deadline, cancellation, and collection errors to callers. A server without the new method must report an unsupported operation rather than fabricated zero values. Generate protocol artifacts and update the service documentation together. Preserve aggregate scope unless a separate database selector is explicitly added; callers must not interpret aggregate counts as database-local values.

Keep this operation bounded to a snapshot of local statistics. It should not scan document storage or wait for indexes to catch up. Correlate failures with the remote endpoint and operation through the shared diagnostics conventions, without including document contents.

## Alternatives

**Derive statistics from Health/GetState.** This can recover some index counts but cannot reconstruct events-applied or last-event timing faithfully and couples separate response meanings.

**Use only a metrics exporter.** Exported metrics support operational aggregation, but do not satisfy callers of the existing Stats interface. Exporter work remains independently useful.

## Acceptance Criteria

- For the same service snapshot, local and remote calls return the same field values and units.
- A known applied-event sequence produces nonzero remote counters and the expected last-event state.
- Empty service state, restart counter reset, unavailable server, unsupported server, and canceled request have distinct documented outcomes.
- Concurrent statistics reads and event application remain race-free, with no document-storage reads.

## Risks

Process restarts reset counters; dashboards must account for that lifecycle. Proto fields need widths that preserve current Go values without truncation. New metrics such as lag or rebuild duration must not be invented from these existing counters.

## Dependencies

[Application observability](../architecture/2026-09-07-application-observability.md) owns exporter and telemetry-wide decisions; this proposal owns parity of the existing callable statistics interface.
