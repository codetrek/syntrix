# Realtime Watching Mechanism Discussion

**Date:** December 15, 2025
**Topic:** Realtime Push Architecture and Change Stream Processing

## Scope & Assumptions
- Single gateway, single port; realtime endpoints: `/realtime/ws` (WebSocket) and `/realtime/sse` (SSE).
- Watch expressions: reuse the query/filter language from 002 (CEL subset) as the watch matcher to keep semantics and safety consistent.
- Scale targets: aim for up to ~1M concurrent connections and ~10k events/s; initial phase at ~1/10 scale (~100k connections, ~1k events/s) with linear scaling headroom.
- Broadcast policy: small-scope broadcast is acceptable as a fallback; prefer directed routing first.
- Client protocol: primarily WebSocket with brief-disconnect resume; SSE is supported for server-to-client streaming with same auth/semantics.

## 1) Change Stream Processor (CSP) Internals
- Ingestion (Why: throughput and stability; How):
  - Subscribe to MongoDB change streams per collection; CSP nodes consume partitions by (collection, hash(documentKey)) to avoid single hot spots.
  - Pull in batches with rate limits (events/s, bytes/s) and server heartbeat checks.
- Resume & durability (Why: lossless recovery; How):
  - Persist resume token per partition (etcd/embedded KV/DB) with periodic checkpoints plus on-commit writes.
  - Recover per partition on restart/rebalance; if the gap exceeds retention, trigger a full rescan or ask clients to resync.
- Dedup (Why: avoid duplicate pushes; How):
  - Rely on resume token monotonicity; across partitions or replay windows use a short-TTL cache keyed by (clusterTime, txnId, documentKey).
- Bloom/index hygiene defaults (Why: predictable FP rate; How):
  - Initial Bloom target FP rate 0.5–1% per collection with bits/key ~10 and k ~7; rebuild every 24h or when FP sampling >2% to keep memory bounded and drift low.
- Ordering (Why: predictable client ordering; How):
  - Keep event order within a partition and attach a per-partition increasing seq downstream.
- Gap handling (Why: avoid silent data loss; How):
  - Track per-partition lag; if resume token gap > retention window, mark the partition as “stale” and refuse delivery until a catch-up scan or client resync occurs.
  - When stale, surface an operator alert plus a client-visible `resync-required` status so downstream can reconcile.

Example CSP loop (Go-style pseudocode):
```go
for evt := range stream {
    part := partitionKey(evt.Collection, evt.DocumentKey)
    if !shouldProcess(part) { continue }

    if seen := dedupCache.Has(part, evt.ClusterTime, evt.TxnID, evt.DocumentKey); seen {
        continue
    }
    dedupCache.Add(part, evt.ClusterTime, evt.TxnID, evt.DocumentKey)

    seq := seqGen.Next(part)
    checkpointAsync(part, evt.ResumeToken)

    candidates := matchIndex.Lookup(part, evt) // fast path + Bloom
    for _, sub := range candidates {
        if matchExact(sub.Expr, evt) {
            enqueue(sub.Gateway, Delivery{
                SubscriptionID: sub.ID,
                Seq:            seq,
                Partition:      part,
                LSN:            evt.LSN,
                Payload:        evt.Doc,
            })
        }
    }
}
```

## 2) Query Matching Pipeline
- Matcher language: reuse the expression engine from 002 (CEL subset) supporting field filters, ranges, and auth context.
- Pipeline (Why: reduce per-event compute; How):
  1) Fast path: documentKey exact subscriptions (primary-key watches).
  2) Probabilistic filter: per-collection field hashes into Bloom/segmented Bloom; tunable parameters (initial phase uses conservative bits/k with headroom).
  3) Exact match: evaluate survivors with the expression engine.
- Subscription index (Why: reduce broadcast; How):
  - Gateway registers active subscription summaries (field filters, tenant, subscription id) to CSP; CSP maintains per-collection indexes with periodic refresh/expiry.
  - Bloom false positives are filtered by the Gateway (small-scope broadcast remains an acceptable fallback).
- Index hygiene (Why: keep match quality stable; How):
  - Enforce TTL on inactive summaries; rebuild Blooms periodically to limit false-positive drift; size budgets per collection to prevent single-tenant blowup.

Example of building a subscription index entry:
```go
type SubscriptionSummary struct {
    ID        string
    Tenant    string
    Gateway   string
    Expr      cel.Ast // parsed from watch expression
    BloomKeys []uint64
}

func buildSummary(sub SubscribeRequest) SubscriptionSummary {
    keys := bloomKeysFromExpr(sub.Expr, bloomConfig)
    return SubscriptionSummary{
        ID:        sub.ID,
        Tenant:    sub.Tenant,
        Gateway:   sub.Gateway,
        Expr:      compileToCEL(sub.Expr),
        BloomKeys: keys,
    }
}
```

## 3) Routing & Distribution
- Topology (Why: horizontal scale; How):
  - CSP and Gateway clusters both use a consistent-hash ring; events route by (collection + docKey hash) to the responsible Gateway by default.
  - If the routing key is uncertain (e.g., range queries), CSP can broadcast to a small candidate set of Gateways; Gateways then filter locally.
- Membership (Why: elasticity and migration; How):
  - Control plane maintains heartbeats and leases; ring changes trigger smooth partition migration (drain plus resume token handoff).
- Rebalance semantics (Why: avoid gaps/dupes during movement; How):
  - Donor drains the partition queue, checkpoints resume token, transfers the last committed token to the recipient, and enters a brief “handoff broadcast” mode (up to 1s) so no events are lost; recipient starts from the token and exits broadcast once caught up.
- Backpressure (Why: prevent amplification; How):
  - Gateways enforce queue watermarks and rate limits; on overflow they fall back to conservative broadcast or request client reconnect/slowdown.
- Backpressure thresholds (Why: predictable degradation; How):
  - Default per-Gateway watermark at 70% soft, 90% hard. Soft triggers shed optional load (low-priority subscriptions); hard triggers switch the affected partition to “broadcast + client resync required” until drained below 60%.
- Degradation (Why: preserve correctness under stress; How):
  - If Gateway or CSP lag exceeds threshold, switch that partition to “broadcast + client resync required” mode until queues drain; emit alerts and shed optional load (non-critical subs) before dropping any events.
  - Broadcast trigger (Why: bound fan-out choices; How): if match index lookup latency p99 > 50ms or FP rate sampling >5% for a partition, temporarily expand candidate Gateways to a fixed fan-out (e.g., 3) and reevaluate after 1m.

## 4) Client Protocol (WebSocket / SSE)
- Endpoints: `/realtime/ws` (bidi WebSocket), `/realtime/sse` (server-to-client SSE).
- Connection: authenticate then establish stream; client declares tenant/project context.
- Subscription management: client submits watch expressions; server returns subscription ids and current matcher version.
- Delivery & sequencing: each event includes (subscription ids, seq, partition id, lsn). Gateway preserves per-partition order.
- Brief-disconnect resume: client keeps last seq/lsn; on reconnect (WS) it sends a resume token; server replays from a sliding window (in-memory/short TTL). Outside the window, client performs resync. SSE clients reconnect with last known cursor if supported.
- Ack: optional client ack (WS) supports flow control and window trimming on the Gateway.
- Sliding window (Why: bounded memory with useful coverage; How):
  - Default window: time-based 3 minutes or size-based last 10k events per partition, whichever is smaller; cap memory at ~32 MB per partition group with LRU eviction; clients exceeding the window get a `resync-required` status.
- Message schema (Why: interoperability and evolvability; How):
  - Include protocol `version`, `tenant`, `subIds`, `seq`, `partition`, `lsn`, `payload`, optional `checksum`; reserve an `extensions` map for forward-compatible fields.

Client brief-disconnect resume (protocol sketch):
```json
// Client -> Gateway (reconnect)
{ "type": "resume", "token": "<resume-token>", "lastSeq": 12345 }

// Gateway -> Client
{ "type": "resume-ack", "status": "ok", "fromSeq": 12346 }

// Gateway replays from sliding window [12346, latest], else asks for full resync
```

## 5) Reliability & Observability
- Reliability: end-to-end at-least-once from CSP to Gateway to clients; dedupe via seq + subscription id; heartbeat/keepalive to detect dead links.
- Observability:
  - Metrics: per-partition lag, events/s, match hit rate, Bloom false-positive rate, broadcast ratio, queue watermarks, reconnect/resume success.
  - Audit: subscription create/destroy, routing changes, broadcast reasons, resume failures.
  - Sampled logs: slow matchers (expression latency), large subscription sets, repeated replays.
- SLO hooks (Why: actionable signals; How):
  - Define target p99 delivery latency per partition and max acceptable Bloom FP rate; alerts tie directly to degradation policy in section 3.

## 6) Scalability Plan
- Compute: CSP and Gateway both scale horizontally; partitions >= node count for rebalancing; Bloom/index parameters adjust with growth.
- Data: subscription indexes and Blooms can be sharded by collection to avoid single-node memory blowup.
- Migration: when nodes change, resume tokens and subscription indexes move with partitions for seamless recovery.

## 7) Shadow Validation / Replay (Note)
- Reminder: matcher/routing changes should support shadow verification or offline replay of recorded change streams to validate correctness without impacting production traffic (details to be added later).
- Shadow plan (Why: validate safely; How):
  - Capture sampled change streams (sanitized) to object storage; replay against a shadow CSP+Gateway deployment running new matcher/routing code; compare match sets and latency, block rollout if divergence exceeds threshold.
