# Frontend App Module

## 1. Overview
The Frontend is a React application that serves as the user interface. It uses RxDB to sync data in real-time from Syntrix, providing a responsive experience without managing WebSocket connections manually.

## 2. Database Layer (`db.ts`)

### 2.1 Schemas
We define two primary schemas for RxDB.

**Chat Schema** (`chats`)
```typescript
{
  "version": 0,
  "primaryKey": "id",
  "type": "object",
  "properties": {
    "id": { "type": "string", "maxLength": 100 },
    "title": { "type": "string" },
    "updatedAt": { "type": "number" }
  }
}
```

**Message Schema** (`messages`)
```typescript
{
  "version": 0,
  "primaryKey": "id",
  "type": "object",
  "properties": {
    "id": { "type": "string", "maxLength": 100 },
    "role": { "type": "string" }, // 'user' | 'assistant'
    "content": { "type": "string" },
    "createdAt": { "type": "number" }
  }
}
```

### 2.2 Sync Strategy
-   **Global Sync**: On startup, sync `users/demo-user/orch-chats` to populate the sidebar.
-   **Dynamic Sync**: When a user selects a chat, dynamically create an RxCollection for `users/demo-user/orch-chats/<chatId>/messages` and start replication. This ensures we only load relevant messages.

## 3. UI Components

### 3.1 `Sidebar`
-   **Data Source**: `db.chats.find().sort({ updatedAt: 'desc' })`
-   **Actions**:
    -   Select Chat: Updates global `activeChatId`.
    -   New Chat: Creates a new document in `chats` collection.

### 3.2 `ChatWindow`
-   **Data Source**: `db['messages-' + activeChatId].find().sort({ createdAt: 'asc' })`
-   **Rendering**:
    -   Renders messages in a scrollable list.
    -   Distinguishes between User (right aligned) and Assistant (left aligned) styles.
    -   (Optional) Renders "Thinking..." indicator if a `toolcall` is pending (can be observed via a separate query or inferred).

### 3.3 `MessageInput`
-   **Action**: Creates a new document in the active message collection with `role: 'user'`.
-   **ID Generation**:
    -   The frontend generates a UUID v4 for the message ID.
    -   **Security Risk**: In a production app, client-generated IDs can be risky if not validated. Malicious users could overwrite existing messages. For this demo, we accept this risk.
