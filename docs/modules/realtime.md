# Realtime Service Module

**Package**: `internal/realtime`

The Realtime Service manages persistent client connections and broadcasts updates.

## Components

### 1. Hub (`hub.go`)
-   **Connection Management**: Tracks active clients (WebSocket/SSE).
-   **Broadcasting**: Receives events from the backend and pushes them to connected clients.
-   **Concurrency**: Uses a central loop to handle register/unregister/broadcast actions thread-safely.

### 2. Server (`server.go`)
-   **Endpoints**:
    -   `/v1/realtime`: Supports both WebSocket and SSE (Server-Sent Events) based on the `Accept` header.
-   **Background Tasks**: Starts the Hub and a Watcher that consumes events from the Query Service/CSP.

### 3. Protocols
-   **WebSocket**: Bidirectional communication.
-   **SSE**: Unidirectional (Server-to-Client) text stream.

## Data Flow

1.  `Server` starts a background watcher via `queryService.WatchCollection`.
2.  Events received from the watcher are sent to `Hub.Broadcast()`.
3.  `Hub` iterates over active clients and sends the payload.
