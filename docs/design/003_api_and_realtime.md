# API & Realtime Protocol Discussion

**Date:** December 13, 2025
**Topic:** Detailed API Specification

## 1. REST API Specification

Base URL: `/v1`

### 1.1 Document Operations

#### Get Document
`GET /v1/{collectionPath}/{docID}`

**Response (200 OK):**
```json
{
  "id": "msg-1",
  "collection": "messages",
  "updated_at": 1678888888000,
  "created_at": 1678888888000,
  "version": 1,
  "text": "Hello World",
  "sender": "alice"
}
```

#### Create Document
`POST /v1/{collectionPath}`

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
  "updated_at": 1678888888000,
  "created_at": 1678888888000,
  "version": 1,
  "text": "Hello World",
  "sender": "bob"
}
```

#### Create/Replace Document (Upsert)
`PUT /v1/{collectionPath}/{docID}`

**Request:**
```json
{
  "text": "Hello World Updated",
  "sender": "alice"
}
```

#### Update Document (Patch)
`PATCH /v1/{collectionPath}/{docID}`

**Request:**
```json
{
  "read": true
}
```

#### Delete Document
`DELETE /v1/{collectionPath}/{docID}`

**Response (204 No Content)**

### 1.2 Query Operations

#### Execute Query
`POST /v1/query`

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

### 1.3 Batch Operations

#### Batch Write
`POST /v1/batch`

**Request:**
```json
{
  "operations": [
    {
      "type": "set",
      "path": "users/alice",
      "data": { "status": "offline" }
    },
    {
      "type": "delete",
      "path": "users/bob"
    }
  ]
}
```

## 2. Realtime Protocol (WebSocket)

Endpoint: `ws://host/v1/realtime`

### 2.1 Message Structure

All messages follow a standard JSON envelope:
```json
{
  "id": "request-id",
  "type": "message-type",
  "payload": { ... }
}
```

### 2.2 Authentication

**Client -> Server:**
```json
{
  "id": "1",
  "type": "auth",
  "payload": {
    "token": "jwt-token-here"
  }
}
```

**Server -> Client:**
```json
{
  "id": "1",
  "type": "auth_ack",
  "payload": {
    "status": "ok"
  }
}
```

### 2.3 Live Query (Subscription)

**Client -> Server (Subscribe):**
```json
{
  "id": "sub-1",
  "type": "subscribe",
  "payload": {
    "query": {
      "collection": "rooms/room-1/messages",
      "filters": [{"field": "timestamp", "op": ">", "value": 1678888888000}]
    }
  }
}
```

**Server -> Client (Event):**
```json
{
  "type": "event",
  "payload": {
    "subId": "sub-1",
    "delta": {
      "op": "insert",
      "doc": { "path": "rooms/room-1/messages/m3", "data": {...}, "version": 1 }
    }
  }
}
```

### 2.4 Replication Stream (RxDB)

**Client -> Server (Start Stream):**
```json
{
  "id": "stream-1",
  "type": "stream",
  "payload": {
    "collection": "rooms/room-1/messages",
    "checkpoint": {
      "updated_at": 1678888888000,
      "id": "last-doc-id"
    }
  }
}
```

**Server -> Client (Stream Event):**
```json
{
  "type": "stream-event",
  "payload": {
    "streamId": "stream-1",
    "documents": [ ... ],
    "checkpoint": {
      "updated_at": 1678889999000,
      "id": "new-last-doc-id"
    }
  }
}
```

### 2.5 Unsubscription

**Client -> Server:**
```json
{
  "id": "req-2",
  "type": "unsubscribe",
  "payload": {
    "id": "sub-1" // The ID of the subscription or stream to cancel
  }
}
```

**Server -> Client (Unsubscribe Ack):**
```json
{
  "id": "req-2",
  "type": "unsubscribe_ack",
  "payload": {
    "status": "ok"
  }
}
```

## 3. RxDB Replication Protocol (HTTP)

### 3.1 Pull Changes
`POST /v1/replication/pull`

**Request:**
```json
{
  "collection": "room/chatroom-1/messages",
  "checkpoint": {
    "updated_at": 1678888888000,
    "id": "last-doc-id"
  },
  "limit": 100
}
```

**Response:**
```json
{
  "documents": [ ... ],
  "checkpoint": {
    "updated_at": 1678889999000,
    "id": "new-last-doc-id"
  }
}
```

### 3.2 Push Changes
`POST /v1/replication/push`

**Request:**
```json
{
  "collection": "room/chatroom-1/messages",
  "changes": [
    {
      "doc": { "path": "room/chatroom-1/messages/m1", "data": {...}, "version": 1 }
    }
  ]
}
```

**Response:**
```json
{
  "conflicts": [] // Empty if success, otherwise returns conflicting server docs
}
```
