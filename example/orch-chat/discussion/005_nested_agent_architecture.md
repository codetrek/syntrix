# 005_nested_agent_architecture.md

## Overview

This document defines the **Nested Agent Architecture** for the Chat App.
In this model, each Sub-agent is treated as a first-class entity with its own dedicated storage space (messages, tool calls, state), nested under the main chat.

## 1. Data Model (Schema)

We utilize Syntrix's hierarchical collection capabilities.

### A. Main Chat Level
*   **Collection**: `users/*/orch-chats`
*   **Collection**: `users/*/orch-chats/*/messages` (User & Orchestrator interaction)
*   **Collection**: `users/*/orch-chats/*/tasks` (The "link" to sub-agents)

**Task Schema (`tasks`)**:
```typescript
type AgentTask = {
    id: string;          // Task ID
    userId: string;
    chatId: string;

    // Task Definition
    type: 'agent' | 'tool';
    name: string;        // e.g., "agent:researcher", "tool:calculator"
    instruction: string; // The prompt from Orchestrator

    // Link to Sub-Agent (if type == 'agent')
    subAgentId?: string;

    // State
    status: 'pending' | 'running' | 'success' | 'failed';
    result?: string;

    // UI Link
    triggerMessageId: string; // Which user message caused this?

    createdAt: number;
    updatedAt: number;
};
```

### B. Sub-Agent Level
*   **Collection**: `users/*/orch-chats/*/sub-agents` (The Agent Instance)
*   **Collection**: `users/*/orch-chats/*/sub-agents/*/messages` (Internal Monologue & Tool Results)
*   **Collection**: `users/*/orch-chats/*/sub-agents/*/tool-calls` (Tools used by this agent)

**SubAgent Schema (`sub-agents`)**:
```typescript
type SubAgent = {
    id: string;
    userId: string;
    chatId: string;
    taskId: string;      // Back-link to the parent Task

    role: 'general' | 'researcher';
    status: 'active' | 'completed' | 'failed' | 'waiting_for_user';

    // Context Summary (Optional, for long running agents)
    summary?: string;

    createdAt: number;
    updatedAt: number;
};
```

**SubAgentMessage Schema (`sub-agents/*/messages`)**:
```typescript
type SubAgentMessage = {
    id: string;
    userId: string;
    subAgentId: string;
    role: 'system' | 'user' | 'assistant' | 'tool';
    content: string;

    // If this message triggered a tool call
    toolCallId?: string;

    createdAt: number;
};
```

**SubAgentToolCall Schema (`sub-agents/*/tool-calls`)**:
```typescript
type SubAgentToolCall = {
    id: string;
    userId: string;
    subAgentId: string;
    toolName: string;    // e.g., "tavily_search"
    args: any;
    status: 'pending' | 'success' | 'failed';
    result?: string;
    error?: string;

    createdAt: number;
};
```

## 2. Triggers & Workflow

### Flow 1: User Request -> Orchestrator
1.  **User** sends message.
2.  **Trigger**: `on-user-message` (Target: `messages`).
3.  **Orchestrator Worker**:
    *   Analyzes intent.
    *   **Decision**:
        *   **New Task**: Creates a document in `tasks` collection: `{ type: 'agent', name: 'agent:researcher', instruction: '...' }`.
        *   **Reactivate**: Updates existing `SubAgent` and adds new user message.

### Flow 2: Task Creation -> Sub-Agent Initialization
1.  **Trigger**: `on-new-task` (Target: `tasks`).
    *   Condition: `type == 'agent'`.
2.  **Agent Runner Worker**:
    *   Creates a new `SubAgent` document: `sub-agents/<agent-id>`.
    *   Updates `tasks` doc with `subAgentId`.
    *   Inserts initial `system` and `user` (instruction) messages into `sub-agents/<agent-id>/messages`.

### Flow 3: Sub-Agent Execution Loop
1.  **Trigger**: `on-sub-agent-message` (Target: `sub-agents/*/messages`).
    *   Condition: `role == 'user'` OR `role == 'tool'`.
2.  **Agent Logic Worker**:
    *   Reads full history from `sub-agents/<agent-id>/messages`.
    *   Calls LLM.
    *   **Decision**:
        *   **Call Tool**: Creates doc in `sub-agents/<agent-id>/tool-calls`.
        *   **Final Answer**:
            *   Writes `assistant` message to `sub-agents/<agent-id>/messages`.
            *   Updates `sub-agents/<agent-id>` status to `completed`.
            *   Updates parent `tasks` doc with `result` and `status: success`.
        *   **Ask User**:
            *   Writes `assistant` message (question).
            *   Updates `sub-agents/<agent-id>` status to `waiting_for_user`.
            *   Updates parent `tasks` doc with `status: waiting`.

### Flow 4: Tool Execution
1.  **Trigger**: `on-sub-agent-tool` (Target: `sub-agents/*/tool-calls`).
2.  **Tool Worker**:
    *   Executes tool (e.g., Tavily).
    *   Updates `tool-calls` doc with result.
    *   **Crucial Step**: Inserts a new message to `sub-agents/<agent-id>/messages` with `role: tool` and content = result. (This triggers Flow 3 again).

### Flow 5: Completion -> Orchestrator
1.  **Trigger**: `on-task-completed` (Target: `tasks`).
    *   Condition: `status == 'success'`.
2.  **Orchestrator Worker**:
    *   Reads the task result.
    *   Generates final response to user in `orch-chats/*/messages`.

## 3. Frontend UX

### Sidebar
*   Displays the `tasks` list under each chat.
*   Shows status icon.

### Chat Window
*   **Main View**: Shows User messages and Orchestrator responses.
*   **Task Block**: Rendered under the triggering message.
    *   Shows "Agent: Researcher - Completed".
    *   **Click Action**: Opens a **"Sub-Agent Inspector"** (Modal or Split View).

### Sub-Agent Inspector
*   This is a full-featured Chat UI.
*   Binds to `sub-agents/<agent-id>/messages`.
*   Shows the internal monologue, tool inputs/outputs, and intermediate thoughts.
*   Read-only for the user.
