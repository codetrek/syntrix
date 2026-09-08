# Evaluator Service Design

## Overview

The Evaluator Service watches document changes, evaluates trigger conditions, and publishes matched tasks to the delivery queue.

## Responsibility

- Watch document changes from Puller
- Filter events by database
- Evaluate trigger conditions using CEL
- Build and publish DeliveryTask to NATS JetStream
- Manage checkpoint for resume capability

## Data Flow

```
┌──────────────────────────────────────────────────────────────────────┐
│                      Evaluator Service                                │
│                                                                       │
│  ┌──────────┐    ┌─────────┐    ┌───────────┐    ┌──────────────┐   │
│  │ Watcher  │ -> │   CEL   │ -> │  Builder  │ -> │  Publisher   │   │
│  │(Puller)  │    │Evaluator│    │(DelivTask)│    │(NATS JS)     │   │
│  └──────────┘    └─────────┘    └───────────┘    └──────────────┘   │
│       │                                                    │         │
│       v                                                    v         │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │                      Checkpoint Store                         │   │
│  │                 (sys/checkpoints/trigger_evaluator)          │   │
│  └──────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────┘
```

## Service Interface

```go
// Service evaluates document changes against trigger rules and publishes matched tasks.
type Service interface {
    // LoadTriggers validates and loads trigger rules.
    LoadTriggers(triggers []*types.Trigger) error

    // Start begins watching for changes and evaluating triggers.
    // Blocks until context is cancelled.
    Start(ctx context.Context) error

    // Close releases resources.
    Close() error
}

// ServiceOptions configures the evaluator service.
type ServiceOptions struct {
    Database     string  // Syntrix logical database for event filtering
    StartFromNow bool    // If true, start from "now" when checkpoint missing
    RulesFile    string  // Path to trigger rules file (JSON/YAML)
    StreamName   string  // NATS stream name (default: "TRIGGERS")
}

// Dependencies contains external dependencies for the evaluator service.
type Dependencies struct {
    Store   storage.DocumentStore
    Puller  puller.Service
    Nats    *nats.Conn
    Metrics types.Metrics
}
```

## Components

### 1. DocumentWatcher

The watcher subscribes to Puller and emits filtered change events.

**Key behaviors:**
- Subscribes to Puller with a consumer ID
- Filters events by `Database` field (Syntrix logical database)
- Transforms `PullerEvent` to `SyntrixChangeEvent`
- Emits business `delete` events for retained tombstones from MongoDB updates or replacements
- Ignores MongoDB physical document deletes through `ErrDeleteOPIgnored`
- Manages checkpoint for resume capability

See: [01.checkpoint.md](01.checkpoint.md)

#### Document deletion

Syntrix logical deletion writes `deleted=true`, clears business `data` to `{}`,
and retains document metadata. The watcher keeps this tombstone in
`SyntrixChangeEvent.Document` and classifies it as `delete`. MongoDB physical
document deletion, including later tombstone cleanup, does not produce a
business trigger event. The [storage deletion contract](../../core/storage/03.stores.md#document-deletion-and-physical-cleanup)
owns the distinction and retention behavior.

The current Puller path does not capture previous document images, so `Before`
is absent. CEL sees the tombstone's `id`, `collection`, and `version` under
`event.document`, with no previous business fields and `event.before=null`.
Task construction uses the tombstone's empty `Data` map as `Payload`; JSON
serialization omits this empty `payload` field. Matching and delivery still
carry the business `delete` type and document routing metadata.

See [CEL evaluation](02.cel_evaluator.md#document-deletion-and-before-images) for
supported conditions and the proposed before-image capability.

### 2. CEL Evaluator

Evaluates trigger conditions against events.

**Key behaviors:**
- Compiles CEL expressions once and caches programs
- Evaluates conditions against event data
- Supports `path.Match` for collection glob matching

### 3. TaskPublisher

Publishes matched tasks to NATS JetStream.

**Key behaviors:**
- Publishes to subject: `<stream>.<database>.<collection>.<docKey>`
- Uses subject-safe encoding for docKey (base64url without padding)
- Hashes docKey if subject would exceed NATS limit (1024 bytes)

## Configuration

```go
type TriggerConfig struct {
    // Evaluator-specific
    Database     string  // Syntrix logical database
    StartFromNow bool    // Start from now if checkpoint missing
    RulesFile    string  // Trigger rules file path

    // Shared
    StreamName   string  // NATS stream name
}
```

## Implementation

See: `internal/trigger/evaluator/`
- `service.go` - Service interface and implementation
- `factory.go` - Factory function
- `validation.go` - Trigger validation
