# Evaluator Service Design

The Evaluator Service converts document changes into matched delivery tasks.
Separating evaluation from HTTP delivery gives each service its own capacity and
lifecycle while preserving document routing and resumable input.

## Data Flow

```text
Puller changes
      |
      v
+---------------------+
| Business event gate |
+---------------------+
      |               \ physical document removal
      |                +----> No business trigger event
      v
Database / collection / event-type filters
      |
      v
CEL condition ---- no match ----> No task
      |
      | match
      v
Delivery task ----> NATS JetStream ----> Delivery Service

Puller progress ----> Evaluator checkpoint store ----> Resume position
```

The [checkpoint design](01.checkpoint.md) owns progress persistence and resume
behavior. Receiving progress does not itself establish successful delivery.

## Component Contracts

| Component | Responsibility | Boundary |
|---|---|---|
| Watcher | Subscribe with a consumer identity, select the logical database, and produce business events | Physical document removal produces no business event |
| CEL evaluator | Match database, collection pattern, event type, and condition | Return match, no match, or evaluation failure; see [CEL evaluation](02.cel_evaluator.md) |
| Task builder | Carry the matched event type, document routing metadata, and available business payload | Empty deleted data cannot supply previous business fields |
| Publisher | Queue tasks in NATS JetStream | Subject: `<stream>.<database>.<collection>.<docKey>`; see [task publishing](03.publisher.md) |
| Checkpoint store | Persist the evaluator's Puller progress for restart | Global storage key: `sys/checkpoints/trigger_evaluator` |

Publisher routing encodes the document key as base64url without padding. A
subject exceeding 1024 bytes uses a hashed key fragment; the task retains the
original routing identity.

## Deletion Contract

The [storage deletion lifecycle](../../core/storage/03.stores.md#document-deletion-and-physical-cleanup)
owns tombstone retention and physical cleanup.

| Source change | Business event | Document state |
|---|---|---|
| Logical deletion: MongoDB update or replacement with `deleted=true` | `delete` | Retained tombstone; business `data={}` and document metadata preserved |
| Physical document removal, including tombstone cleanup | None | No second logical deletion or trigger invocation |

### Current Payload Availability

| Consumer | Logical-delete payload | Limit |
|---|---|---|
| CEL | `event.type="delete"`; `event.document` contains stored `id`, `collection`, and `version` | Business fields and the storage `deleted` flag are absent |
| CEL previous image | `event.before=null` | Current production input has no historical image; `includeBefore` does not enable capture |
| Delivery task | `delete` type and routing metadata; empty business payload | JSON omits the empty `payload` field; deleted business data is unavailable |

[Before-image capture](../../../../../.agents/notes/proposed/feature/2026-09-07-trigger-before-images.md)
remains proposed. It owns capture, retention, transport, per-rule exposure, and
failure behavior when a required historical image is unavailable.

## Service Lifecycle and Configuration

| Operation | Contract |
|---|---|
| Load rules | Validate and activate trigger definitions |
| Start | Watch and evaluate until context cancellation; report failures |
| Close | Release resources owned by the evaluator |

| Setting | Purpose |
|---|---|
| `Database` | Syntrix logical database used for event filtering |
| `StartFromNow` | Permit starting at the current boundary when a checkpoint is missing |
| `RulesFile` | JSON or YAML trigger definitions |
| `StreamName` | Delivery stream name; default `TRIGGERS` |

Dependencies are the document/checkpoint store, Puller subscription, NATS
connection, and metrics sink. HTTP execution belongs to the Delivery Service.
