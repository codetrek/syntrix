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
- Preserves Syntrix logical deletion from tombstone updates; ignores Mongo physical deletion, including later cleanup
- Manages checkpoint for resume capability

See: [01.checkpoint.md](01.checkpoint.md)

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
