# API Gateway Module

**Package**: `internal/api`

The API Gateway is the public-facing interface of Syntrix. It exposes a RESTful API for document management and queries.

## Responsibilities

-   **Routing**: Maps HTTP paths to internal service calls.
-   **Validation**: Validates request parameters (e.g., document paths).
-   **Response Formatting**: Flattens internal document structures into client-friendly JSON.
-   **CORS**: Handles Cross-Origin Resource Sharing headers.

## Endpoints

| Method | Path | Description |
| :--- | :--- | :--- |
| `GET` | `/v1/{path...}` | Retrieve a document by path. |
| `POST` | `/v1/{path...}` | Create a new document. |
| `PUT` | `/v1/{path...}` | Replace a document (Upsert). |
| `PATCH` | `/v1/{path...}` | Update specific fields (Merge). |
| `DELETE` | `/v1/{path...}` | Delete a document. |
| `POST` | `/v1/query` | Execute a structured query. |
| `GET` | `/health` | Health check endpoint. |

## Implementation Details

-   **Server**: `api.Server` wraps an `http.ServeMux`.
-   **Dependency**: Depends on `query.Service` interface. This allows it to talk to a local `query.Engine` or a remote `query.Client`.
-   **Path Validation**: Enforces rules on document paths (e.g., must be `collection/id` pairs).
