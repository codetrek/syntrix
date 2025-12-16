# CSP (Change Stream Processor) Module

**Package**: `internal/csp`

The CSP module is responsible for ingesting realtime changes from the database and exposing them to other internal services.

## Responsibilities

-   **Ingestion**: Connects to MongoDB Change Streams.
-   **Filtering**: (Future) Filters events based on subscription rules.
-   **Distribution**: Exposes an HTTP stream endpoint (`/internal/v1/watch`) for other services (like Realtime Gateway) to consume.

## API

-   `POST /internal/v1/watch`: Starts a streaming response (NDJSON) of change events for a specific collection.

## Implementation

-   **Server**: `csp.Server` handles HTTP requests.
-   **Storage Integration**: Uses `storage.StorageBackend.Watch()` to get the raw event channel.
-   **Streaming**: Writes events to the HTTP response body as they arrive, using `http.Flusher` to ensure immediate delivery.
