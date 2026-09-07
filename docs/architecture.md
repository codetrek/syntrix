# Syntrix Architecture

Syntrix is a realtime document database whose Go services can run together with
direct calls or separately through gRPC. MongoDB stores documents; PostgreSQL
stores user and database metadata. Gateway exposes REST, WebSocket, and SSE.

## Services

| Service | Responsibility |
|---|---|
| Gateway | Authentication, authorization, HTTP requests, and client connections |
| Query | CRUD and query execution over storage and secondary indexes |
| Indexer | Maintain derived secondary indexes from Puller events |
| Puller | Durably order MongoDB changes and serve live delivery and replay |
| Streamer | Match events to subscriptions and route them to gateways |
| Trigger evaluator | Evaluate CEL rules and publish matched delivery tasks |
| Trigger delivery | Execute Webhooks from the configured PubSub queue |

## Data paths

```text
CRUD:
Client -> Gateway -> Query -> Storage -> MongoDB
                       |
                       +-> Indexer (indexed queries)

Changes:
MongoDB -> Puller -> Sync Pebble commit -> committed memory publication
                                              |
                         +--------------------+--------------------+
                         |                    |                    |
                         v                    v                    v
                      Indexer              Streamer        Trigger evaluator
                                              |                    |
                                              v                    v
                                           Gateway               PubSub
                                              |                    |
                                              v                    v
                                         WS/SSE client       Delivery worker
                                                                   |
                                                                   v
                                                                Webhook
```

Puller consumers receive live events from memory after synchronous persistence.
Saved source/generation/sequence progress enables bounded Pebble replay through
one shared local/gRPC subscription state machine. History expiry or source
continuity loss produces an explicit failure. Puller durability does not imply
durable external-client delivery or exactly-once Webhook effects.

## Deployment and limits

Standalone uses direct service calls and in-memory PubSub. Distributed services
use gRPC, with NATS for Trigger queues. A unified server owns network listeners;
service adapters register against it.

Indexer rebuild integration, Streamer durable restart progress, Trigger outbox,
and external-client resynchronization retain separate ownership. See the
[server architecture](design/server/01.architecture.md),
[Puller architecture](design/server/puller/01.architecture.md), and
[documentation guide](README.md) for implemented contracts and remaining work.
