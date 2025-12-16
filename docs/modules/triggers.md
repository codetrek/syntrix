# Trigger Service Module

**Package**: `internal/trigger`

The Trigger Service enables server-side reactions to database changes. It allows users to define rules (using CEL) that trigger external Webhooks.

## Architecture

The service is split into two distinct roles that can be deployed separately:

### 1. Trigger Evaluator
-   **Role**: The "Producer".
-   **Input**: Database Change Stream.
-   **Logic**:
    -   Loads active `Trigger` configurations.
    -   Evaluates each event against the trigger's CEL condition (`CELEvaluator`).
    -   If matched, creates a `DeliveryTask`.
-   **Output**: Publishes the task to NATS JetStream.

### 2. Trigger Worker
-   **Role**: The "Consumer".
-   **Input**: NATS JetStream (`TRIGGERS` stream).
-   **Logic**:
    -   `Consumer` pulls tasks from the queue.
    -   `DeliveryWorker` executes the HTTP POST request to the user's Webhook URL.
    -   Handles retries and failures (via NATS Ack/Nak).
-   **Security**: Signs payloads using HMAC-SHA256 (`X-Syntrix-Signature`).

## Key Components

-   **`Trigger`**: Configuration struct (Condition, URL, Events, etc.).
-   **`CELEvaluator`**: Compiles and executes Google CEL expressions.
    -   Context: `event.type`, `event.document.data`, etc.
-   **`NatsPublisher`**: Publishes tasks to subjects like `triggers.<tenant>.<collection>.<docKey>`.
-   **`Consumer`**: Manages the NATS subscription and message processing loop.

## Configuration

Enabled via `config.yml` or environment variables:
-   `TRIGGER_NATS_URL`: URL of the NATS server.
