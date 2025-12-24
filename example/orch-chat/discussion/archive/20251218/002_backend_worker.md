# Backend Worker Module

## 1. Overview
The Trigger Worker is a Node.js service responsible for the "brain" of the application. It listens to Syntrix Webhooks and interacts with OpenAI and Tavily.

## 2. Configuration (`triggers.json`)

The worker relies on the following Syntrix triggers:

| Trigger ID | Collection | Event | Condition | Endpoint |
| :--- | :--- | :--- | :--- | :--- |
| `on-user-message` | `users/*/orch-chats/*/messages` | `create` | `role == 'user'` | `/webhook/llm` |
| `on-tool-pending` | `users/*/orch-chats/*/toolcall` | `create` | `status == 'pending'` | `/webhook/tool` |
| `on-tool-success` | `users/*/orch-chats/*/toolcall` | `update` | `status == 'success'` | `/webhook/llm` |

## 3. Component Design

### 3.1 `SyntrixClient`
A wrapper class for Syntrix REST API interactions.
-   **Base URL**: `http://localhost:8080` (Default)
-   **API Format**:
    -   Read Document: `GET /api/v1/{path...}`
    -   Create Document: `POST /api/v1/{path...}` (Body: JSON Data)
    -   Update Document: `PATCH /api/v1/{path...}` (Body: JSON Data)
-   **Methods**:
    -   `fetchHistory(chatPath)`: Retrieves conversation context.
        -   *Implementation Note*: Currently Syntrix API only supports single document retrieval. For the demo, we might need to implement a `query` endpoint or iterate known IDs. **Correction**: Syntrix has a Query API (`POST /api/v1/query`). We should use that.
    -   `postMessage(chatPath, content)`: Writes AI response.
    -   `postToolCall(chatPath, toolData)`: Writes tool execution request.
    -   `updateToolCall(toolPath, result)`: Updates tool execution status.

### 3.2 `LLMHandler` (`/webhook/llm`)
Handles logic for interacting with OpenAI.
-   **Input**: Webhook Payload (JSON).
    ```json
    {
      "triggerId": "on-user-message",
      "event": "create",
      "collection": "users/demo-user/orch-chats/chat-1/messages",
      "docKey": "users/demo-user/orch-chats/chat-1/messages/msg-1",
      "after": {
        "role": "user",
        "content": "Hello",
        "createdAt": 1234567890
      },
      "ts": 1234567890
    }
    ```
-   **Process**:
    1.  **Parse Context**: Extract `chatId` and `userId` from `docKey` or `collection`.
    2.  **Reconstruct History**:
        -   **Query 1**: `POST /api/v1/query` with `collection: users/.../messages`, sort by `createdAt`.
        -   **Query 2**: `POST /api/v1/query` with `collection: users/.../toolcall`, sort by `createdAt`.
        -   **Merge Logic**: Combine both lists into a single timeline based on `createdAt`.
        -   **Format**: Convert to OpenAI Message format.
            -   `messages` -> `user` / `assistant` role.
            -   `toolcall` (pending) -> `assistant` role with `tool_calls`.
            -   `toolcall` (success) -> `tool` role with `tool_call_id` and `content` (result).
    3.  **Safety Check (Max Turns)**:
        -   Count the number of `toolcall` documents in the history.
        -   If `tool_calls > 5` (configurable), append a system message "Tool limit reached" and force a final reply without tools. This prevents infinite loops (LLM -> Tool -> LLM -> Tool...).
    4.  **Call OpenAI**: Send `messages` array + `tools` definition.
    5.  **Handle Response**:
        -   If `tool_calls`: Create new doc in `toolcall` collection.
        -   If `content`: Create new doc in `messages` collection.

### 3.3 `ToolRunner` (`/webhook/tool`)
Handles execution of external tools.
-   **Input**: Webhook Payload.
    ```json
    {
      "triggerId": "on-tool-pending",
      "after": {
        "toolName": "tavily_search",
        "args": { "query": "weather in Tokyo" },
        "status": "pending"
      }
    }
    ```
-   **Process**:
    1.  Identify tool (e.g., `tavily_search`).
    2.  Execute tool with provided arguments.
    3.  Update `toolcall` document:
        -   Set `result`: JSON string of tool output.
        -   Set `status`: `success`.
        -   *Note*: This update will trigger `on-tool-success`, which routes back to `LLMHandler`.

## 4. Tech Stack
-   **Runtime**: Node.js
-   **Language**: TypeScript
-   **Framework**: Express
-   **Libraries**: `openai`, `axios`, `dotenv`
