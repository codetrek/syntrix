# API Protocol Design

**Date:** December 13, 2025 (Updated: January 20, 2026)
**Topic:** Detailed API Specification

> **Note:** This document describes the original API structure. See [06.api-url-structure.md](06.api-url-structure.md) for the redesigned URL structure with explicit database paths.

## 1. REST API Specification

Base URL: `/api/v1/databases/{database}/documents` (for document operations)
Base URL: `/api/v1/databases/{database}` (for query operations)

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
  "deleted": false,            // Soft delete marker
  /* other user fields */
}
```

#### Get Document

`GET /api/v1/databases/{database}/documents/{collectionPath}/{id}`

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

`POST /api/v1/databases/{database}/documents/{collectionPath}`

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

`PUT /api/v1/databases/{database}/documents/{collectionPath}/{id}`

**Request:**

```json
{
  "text": "Hello World Updated",
  "sender": "alice"
}
```

#### Update Document (Patch)

`PATCH /api/v1/databases/{database}/documents/{collectionPath}/{id}`

**Request:**

```json
{
  "read": true
}
```

#### Delete Document

`DELETE /api/v1/databases/{database}/documents/{collectionPath}/{id}`

Response (204 No Content)

| Concern | Contract |
| --- | --- |
| Stored state | Retain a `deleted=true` tombstone and its document identity and routing metadata |
| Business data | Clear the document's former business fields |
| Version and time | Advance the version and modification time; assign a cleanup deadline |
| Ordinary reads | Hide the tombstone |
| Business change | Deliver logical deletion through the change stream; expose retained tombstones to replication |
| Physical cleanup | Remove the expired record separately; suppress a second business deletion notification |

```text
DELETE request -> retained tombstone -> logical deletion notification
                         |
                    cleanup deadline
                         |
                         v
                  physical removal
                  (no second notification)
```

See [document deletion and physical cleanup](../core/storage/03.stores.md#document-deletion-and-physical-cleanup)
for the lifecycle and layer boundaries.

### 1.2 Query Operations

#### Execute Query

`POST /api/v1/databases/{database}/query`

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

### 1.3 Trigger API

Internal API for trigger webhooks to access data. All endpoints require database in path.

#### Batch Get Documents

`POST /trigger/v1/databases/{database}/get`

Batch document reads by concrete paths.

#### Query Documents

`POST /trigger/v1/databases/{database}/query`

Single query (same shape as public query API).

#### Write Documents

`POST /trigger/v1/databases/{database}/write`

One or more write ops in a single request.

## 2. Usage Examples

### 2.1 Chat Message (REST)

**Create a message:**

```bash
POST /api/v1/databases/prod/documents/rooms/room-123/messages
{
  "senderId": "user-alice",
  "text": "Hello everyone!",
  "timestamp": 1678888888000
}
```

**Query recent messages:**

```bash
POST /api/v1/databases/prod/query
{
  "collection": "rooms/room-123/messages",
  "filters": [
    {"field": "timestamp", "op": ">", "value": 1678880000000}
  ],
  "orderBy": [{"field": "timestamp", "direction": "desc"}],
  "limit": 50
}
```
