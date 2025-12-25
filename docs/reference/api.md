# Syntrix API Documentation

This document describes the REST API provided by Syntrix.

## Base URL

All API endpoints are prefixed with `/api/v1`, except for the health check.

## Authentication

Syntrix uses JWT (JSON Web Tokens) for authentication.

### Login

Authenticate a user and receive a token pair (Access Token and Refresh Token).

**Endpoint:** `POST /auth/v1/login`

**Request Body:**

```json
{
  "username": "user1",
  "password": "password123"
}
```

**Response (200 OK):**

```json
{
  "access_token": "eyJhbGciOiJIUzI1Ni...",
  "refresh_token": "dGhpcyBpcyBhIHJlZnJlc2ggdG9rZW4...",
  "expires_in": 3600
}
```

### Refresh Token

Get a new Access Token using a valid Refresh Token.

**Endpoint:** `POST /auth/v1/refresh`

**Request Body:**

```json
{
  "refresh_token": "dGhpcyBpcyBhIHJlZnJlc2ggdG9rZW4..."
}
```

**Response (200 OK):**

```json
{
  "access_token": "eyJhbGciOiJIUzI1Ni...",
  "refresh_token": "new_refresh_token...",
  "expires_in": 3600
}
```

### Logout

Invalidate a Refresh Token.

**Endpoint:** `POST /auth/v1/logout`

**Request Body:**

```json
{
  "refresh_token": "dGhpcyBpcyBhIHJlZnJlc2ggdG9rZW4..."
}
```

**Response (200 OK):** Empty body.

## Document Operations

These endpoints allow you to perform CRUD operations on documents.

**Following document fields are reserved by system for special purpose:**

- `id`: Document ID (immutable).
- `version`: Document version (auto-incremented).
- `createdAt`: Database creation timestamp (Unix milliseconds).
- `updatedAt`: Database last update timestamp (Unix milliseconds).
- `collection`: Collection path.

### Get Document

Retrieve a document by its full path.

**Endpoint:** `GET /api/v1/{path...}`

**Example:** `GET /api/v1/rooms/room-1/messages/msg-1`

**Response (200 OK):**

```json
{
  "id": "msg-1",
  "text": "Hello World",
  "sender": "alice",
  "version": 1,
  "createdAt": 1700000000000,
  "updatedAt": 1700000000000,
  "collection": "rooms/room-1/messages"
}
```

### Create Document

Create a new document in a collection. The ID is automatically generated if not provided.

**Endpoint:** `POST /api/v1/{collection_path...}`

**Example:** `POST /api/v1/rooms/room-1/messages`

**Request Body:**

```json
{
  "text": "Hello World",
  "sender": "alice"
}
```

**Response (201 Created):** Returns the created document.

### Replace Document (Upsert)

Replace an existing document or create it if it doesn't exist.

**Endpoint:** `PUT /api/v1/{document_path...}`

**Example:** `PUT /api/v1/rooms/room-1/messages/msg-1`

**Request Body:**

```json
{
  "doc": {
    "id": "msg-1",
    "text": "Hello World Updated",
    "sender": "alice"
  },
  "ifMatch": "Filters"
}
```

**Response (200 OK):** Returns the replaced document.

### Update Document (Patch)

Update specific fields of an existing document.

**Endpoint:** `PATCH /api/v1/{document_path...}`

**Example:** `PATCH /api/v1/rooms/room-1/messages/msg-1`

**Request Body:**

```json
{
  "doc": {
    "text": "Hello World Patched"
  },
  "ifMatch": "Filters"
}
```

**Response (200 OK):** Returns the updated document.

### Delete Document

Delete a document.

**Endpoint:** `DELETE /api/v1/{document_path...}`

**Example:** `DELETE /api/v1/rooms/room-1/messages/msg-1`

**Response (204 No Content):** Empty body.

## Query Operations

Execute complex queries against a collection.

**Endpoint:** `POST /api/v1/query`

* Filters: [filters](./filters.md)

**Request Body:**

```json
{
  "collection": "rooms/room-1/messages",
  "filters": [
    {
      "field": "sender",
      "op": "==",
      "value": "alice"
    }
  ],
  "orderBy": [
    {
      "field": "createdAt",
      "direction": "desc"
    }
  ],
  "limit": 10
}
```

**Response (200 OK):** Array of documents.

## Health Check

Check if the service is running.

**Endpoint:** `GET /health`

**Response (200 OK):** `OK`
