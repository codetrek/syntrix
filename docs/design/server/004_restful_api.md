# API Protocol Design

**Date:** December 13, 2025
**Topic:** Detailed API Specification

## 1. REST API Specification

Base URL: `/api/v1`

### 1.1 Document Operations

#### User-Facing Document (Business Layer Type)

This is the business layer Document type, visible to the API.

```json
{
  "id": "alice",               // Required; immutable once written; allowed charset: [A-Za-z0-9_.-]
  "collection": "chats/chatroom-1/members", // Shadow field, server-written; client input ignored
  "version": 0,                // Shadow field, server-written; client input ignored
  "updatedAt": 1700000000000,  // Shadow field, server-written; client input ignored
  "createdAt": 1700000000000,  // Shadow field, server-written; client input ignored
  /* other user fields */
}
```

#### Get Document

`GET /api/v1/{collectionPath}/{id}`

**Response (200 OK):**

```json
{
  "id": "msg-1",
  "collection": "messages",
  "updatedAt": 1678888888000,
  "createdAt": 1678888888000,
  "version": 1,
  "text": "Hello World",
  "sender": "alice"
}
```

#### Create Document

`POST /api/v1/{collectionPath}`

**Request:**

```json
{
  "text": "Hello World",
  "sender": "bob"
}
```

**Response (201 Created):**

```json
{
  "id": "generated-id-123",
  "collection": "messages",
  "updatedAt": 1678888888000,
  "createdAt": 1678888888000,
  "version": 1,
  "text": "Hello World",
  "sender": "bob"
}
```

#### Create/Replace Document (Upsert)

`PUT /api/v1/{collectionPath}/{id}`

**Request:**

```json
{
  "text": "Hello World Updated",
  "sender": "alice"
}
```

#### Update Document (Patch)

`PATCH /api/v1/{collectionPath}/{id}`

**Request:**

```json
{
  "read": true
}
```

#### Delete Document

`DELETE /api/v1/{collectionPath}/{id}`

Response (204 No Content)

### 1.2 Query Operations

#### Execute Query

`POST /api/v1/query`

**Request:**

```json
{
  "collection": "rooms/room-1/messages",
  "filters": [
    {"field": "timestamp", "op": ">", "value": 1678888888000}
  ],
  "orderBy": [
    {"field": "timestamp", "direction": "desc"}
  ],
  "limit": 20,
  "startAfter": "cursor-string-from-prev-response"
}
```

**Response (200 OK):**

```json
{
  "documents": [ ... ],
  "nextCursor": "cursor-string-for-next-page"
}
```
