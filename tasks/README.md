# Task Index

Index of task planning documents for Syntrix.

## Guidelines
- New documents must include `Date`, `Status`, and `Scope` fields in the header.
- When a task is completed, update the `Status` in both the document and this index.

## Tasks

- [016.2025-12-29-change-stream-puller.md](016.2025-12-29-change-stream-puller.md) — Change Stream Puller (fan-out, checkpoints, JetStream). Status: In Progress. Depends on: 014 and 015.
- [017.2025-12-29-index-layer-implementation.md](017.2025-12-29-index-layer-implementation.md) — Index Layer Implementation (Data/Index/Query). Status: Planned. Depends on: 016.
- [018.2025-12-29-streamer-implementation-plan.md](018.2025-12-29-streamer-implementation-plan.md) — Streamer Service Implementation (JetStream-based event matching & routing). Status: Planning Phase - 23 critical issues identified. **BLOCKED by Task 016 (Puller Service)**. Est. 9-12 weeks after Puller completion.
- [019.2025-12-31-standalone-mode-implementation.md](019.2025-12-31-standalone-mode-implementation.md) — Standalone Mode Implementation (all services in single process). Status: Planning. Est. 3-4 days.
- [020.2025-12-31-puller-design-alignment.md](020.2025-12-31-puller-design-alignment.md) — Puller Design Alignment (replay, coalescing, checkpoints, gaps). Status: Completed. Depends on: 016.
