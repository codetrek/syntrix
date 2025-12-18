# Chat App Architecture

## 1. System Architecture

The system follows a reactive, event-driven pattern. The frontend is a "dumb" view that syncs state, while backend workers handle business logic triggered by database events.

```ascii
+----------------+       +----------------+       +----------------+
|                |       |                |       |                |
|  Frontend App  | <---> |    Syntrix     | <---> | Trigger Worker |
| (React + RxDB) |       |      DB        |       | (Node.js)      |
|                |       |                |       |                |
+-------+--------+       +-------+--------+       +-------+--------+
        |                        ^   ^                    |
        | Write Message          |   | Webhook (Event)    |
        +------------------------+   +--------------------+
                                     |
                                     v
                             +-------+-------+
                             |               |
                             |  LLM / Tools  |
                             |               |
                             +---------------+
```

## 2. Data Flow (The Agent Loop)

1.  **User Input**: Frontend writes `User Message` to Syntrix.
2.  **Trigger 1**: Syntrix detects new message -> Calls Worker.
3.  **LLM Inference**: Worker calls LLM.
    *   *Case A (Reply)*: LLM generates text -> Worker writes `Assistant Message`.
    *   *Case B (Tool)*: LLM requests tool -> Worker writes `Tool Call (pending)`.
4.  **Trigger 2**: Syntrix detects `Tool Call (pending)` -> Calls Worker.
5.  **Tool Execution**: Worker executes tool -> Updates `Tool Call (success)` with result.
6.  **Trigger 3**: Syntrix detects `Tool Call (success)` -> Calls Worker.
7.  **Final Reply**: Worker sends Tool Result to LLM -> LLM generates text -> Worker writes `Assistant Message`.

## 3. Data Model

We use a hierarchical path structure.

```ascii
users/
  └── demo-user/
      └── orch-chats/
          ├── chat-1/
          │   ├── messages/
          │   │   ├── msg-1 (User)
          │   │   └── msg-2 (Assistant)
          │   └── toolcall/
          │       └── call-1 (Search)
          └── chat-2/
              └── ...
```

## 4. UI Layout

```ascii
+---------------------------------------------------------------+
|  Syntrix Chat Demo                                            |
+---------------------+-----------------------------------------+
|  + New Chat         |  Chat: "Weather in Tokyo"               |
|                     |                                         |
|  History            |  [User]                                 |
|  - Weather in To... |  What's the weather like?               |
|  - React Help       |                                         |
|  - Hello World      |  [Agent]                                |
|                     |  Let me check that for you...           |
|                     |  [Tool: Searching "Tokyo Weather"]      |
|                     |                                         |
|                     |  It is currently sunny in Tokyo...      |
|                     |                                         |
|                     |                                         |
|                     |                                         |
|                     +-----------------------------------------+
|                     | [ Type a message... ]           [Send]  |
+---------------------+-----------------------------------------+
```
