# 008_frontend_ux_nested.md

## Overview

This document outlines the UI changes for the Nested Agent Architecture.

## 1. Data Subscription

We need to subscribe to multiple collections.

```typescript
// Main Chat
const messages$ = db.messages.find({ selector: { chatId } }).$;
const tasks$ = db.tasks.find({ selector: { chatId } }).$;

// Sub-Agent Inspector (When opened)
const subAgentMessages$ = db.sub_agent_messages.find({ selector: { subAgentId } }).$;
```

## 2. UI Components

### A. Task Block (In Chat Stream)
*   **Location**: Rendered in the message stream.
    *   **Linkage**: The `Task` document contains `triggerMessageId`, which corresponds to the **User Message** that initiated the task.
    *   **Sorting**: The UI should render the Task immediately after the referenced User Message.
    *   **Implementation**: Maintain a lookup map (e.g., `Map<triggerMessageId, Task>`) to efficiently inject the Task component while iterating over the message list.
*   **Appearance**: A distinct card showing:
    *   **Icon**: Agent Type (Researcher/General).
    *   **Status**: Running/Completed.
    *   **Summary**: "Researching Bitcoin..." (derived from Task instruction).
    *   **Action**: "View Details" button.

### B. Sub-Agent Inspector (Modal/Panel)
*   **Trigger**: Clicking "View Details".
*   **Content**: A full read-only chat interface.
    *   Shows the System Prompt (optional).
    *   Shows the "User" instruction.
    *   Shows "Assistant" thoughts.
    *   Shows "Tool" inputs/outputs (collapsible).
    *   **Input Area**: Enabled ONLY if `SubAgent.status === 'waiting_for_user'`.
*   **Auto-Scroll**: Should auto-scroll as the agent works.

### C. Sidebar
*   **Content**: List of active/recent Tasks.
*   **Interaction**: Clicking a task opens the Inspector directly.

## 3. Discussion Points

1.  **Real-time Updates**: Since we use RxDB, the Inspector will update in real-time as the backend worker inserts messages.
2.  **Read-Only vs Interactive**: The Inspector input box should be disabled by default. It only becomes active when the Agent explicitly requests user input (`status: waiting_for_user`).
