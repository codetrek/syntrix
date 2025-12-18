# 006_schema_and_triggers.md

## Overview

This document details the database schema and trigger configurations for the Nested Agent Architecture.

## 1. Database Schema (`db.ts`)

We need to define schemas for `Task`, `SubAgent`, `SubAgentMessage`, and `SubAgentToolCall`.

### A. Task Schema (`tasks`)
Represents a high-level directive from the Orchestrator.

```typescript
export type AgentTask = {
    id: string;
    userId: string;      // Partition Key. Index: true
    chatId: string;      // Index: true

    // Task Definition
    type: 'agent' | 'tool';
    name: string;        // e.g., "agent:researcher"
    instruction: string; // "Research Bitcoin price..."

    // Linkage
    subAgentId?: string; // Populated when agent starts
    triggerMessageId: string; // UI linkage. Index: true

    // State
    status: 'pending' | 'running' | 'success' | 'failed' | 'waiting';
    result?: string;
    error?: string;

    createdAt: number;
    updatedAt: number;
};
```

### B. Sub-Agent Schema (`sub_agents`)
Represents the lifecycle of a sub-agent instance.

```typescript
export type SubAgent = {
    id: string;
    userId: string;      // Partition Key. Index: true
    chatId: string;
    taskId: string;      // Parent Task ID. Index: true

    role: 'general' | 'researcher';
    status: 'active' | 'completed' | 'failed' | 'waiting_for_user';

    // Safety
    iterationCount: number; // Max loop guard

    createdAt: number;
    updatedAt: number;
};
```

### C. Sub-Agent Message Schema (`sub_agent_messages`)
Stores the internal monologue and tool interactions.

```typescript
export type SubAgentMessage = {
    id: string;
    userId: string;      // Partition Key. Index: true
    subAgentId: string;  // Index: true

    role: 'system' | 'user' | 'assistant' | 'tool';
    content: string;

    // Metadata
    toolCallId?: string; // If role=='tool', which call was this? CRITICAL for OpenAI context.
    toolCalls?: {        // If role=='assistant', the tools it wants to call
        id: string;
        type: 'function';
        function: { name: string; arguments: string };
    }[];
    name?: string;       // For tool outputs

    createdAt: number;
};
```

### D. Sub-Agent Tool Call Schema (`sub_agent_tool_calls`)
Stores specific tool executions (e.g., Tavily).

```typescript
export type SubAgentToolCall = {
    id: string;          // This ID is used as tool_call_id in messages
    userId: string;      // Partition Key. Index: true
    subAgentId: string;  // Index: true

    toolName: string;
    args: any;

    status: 'pending' | 'success' | 'failed';
    result?: string;
    error?: string;

    createdAt: number;
};
```

## 2. Triggers (`triggers.json`)

### 1. Orchestrator Entry
*   **ID**: `on-user-message`
*   **Collection**: `users/*/orch-chats/*/messages`
*   **Event**: `create` (role='user')
*   **Action**: `POST /webhook/orchestrator`

### 2. Sub-Agent Initialization
*   **ID**: `on-new-task`
*   **Collection**: `users/*/orch-chats/*/tasks`
*   **Event**: `create` (type='agent')
*   **Action**: `POST /webhook/agent-init`

### 3. Sub-Agent Loop (The Brain)
*   **ID**: `on-sub-agent-message`
*   **Collection**: `users/*/orch-chats/*/sub-agents/*/messages`
*   **Event**: `create`
*   **Condition**: `event.document.data.role == 'user' || event.document.data.role == 'tool'`
*   **Action**: `POST /webhook/agent-loop`
    *   **CRITICAL**: We MUST NOT trigger on `assistant` messages. Doing so would cause an infinite loop where the agent triggers itself immediately after generating a thought.

### 4. Tool Execution
*   **ID**: `on-sub-agent-tool`
*   **Collection**: `users/*/orch-chats/*/sub-agents/*/tool-calls`
*   **Event**: `create` (status='pending')
*   **Action**: `POST /webhook/tool-runner`

### 5. Task Completion (Feedback Loop)
*   **ID**: `on-task-completed`
*   **Collection**: `users/*/orch-chats/*/tasks`
*   **Event**: `update` (status='success')
*   **Action**: `POST /webhook/orchestrator-finalize`

### 6. Task Waiting (User Input Request)
*   **ID**: `on-task-waiting`
*   **Collection**: `users/*/orch-chats/*/tasks`
*   **Event**: `update` (status='waiting')
*   **Action**: `POST /webhook/orchestrator-notify`

## 3. Indexes & Performance

To ensure efficient querying in RxDB and Syntrix, the following fields MUST be indexed:

1.  **`tasks` Collection**:
    *   `chatId`: Required for filtering tasks by chat.
    *   `userId`: Required for security rules and syncing.
    *   `triggerMessageId`: Required for UI to link tasks to messages.

2.  **`sub_agents` Collection**:
    *   `userId`: Required for security rules and syncing.
    *   `taskId`: Required to find the sub-agent for a given task.

3.  **`sub_agent_messages` Collection**:
    *   `userId`: Required for security rules and syncing.
    *   `subAgentId`: **CRITICAL**. Used to fetch the full context for the LLM.
    *   `createdAt`: Required for sorting messages chronologically.

4.  **`sub_agent_tool_calls` Collection**:
    *   `userId`: Required for security rules and syncing.
    *   `subAgentId`: Required to find pending tool calls for an agent.

## 4. Implementation Notes

### A. RxDB Schema Compliance
*   **JSON Schema**: The TypeScript types above must be converted to valid RxDB JSON Schema.
    *   Use `enum: ['agent', 'tool']` for union types.
    *   Explicitly define `primaryKey`.
    *   Define `indexes` at the top level of the schema.
*   **Relationships**: Treat `subAgentId`, `taskId`, and `chatId` as simple string fields (foreign keys).

### B. Trigger Syntax
*   **Condition Syntax**: Verify the correct syntax for `triggers.json` conditions (CEL vs JS-like). Ensure `event.document.data.role` is accessible.

## 5. Discussion Points

1.  **Collection Naming**: Should we use nested paths in RxDB (e.g. `sub-agents`) or flat collections with IDs?
    *   *Decision*: RxDB works best with flat collections and foreign keys. We will use flat collections: `tasks`, `sub_agents`, `sub_agent_messages`, `sub_agent_tool_calls`.
2.  **Trigger Paths**: Syntrix supports wildcards. `users/*/orch-chats/*/sub-agents/*/messages` is valid.
