# 007_agent_logic.md

## Overview

This document defines the internal logic for the Orchestrator and Sub-Agents.

## 1. Orchestrator Logic (`/webhook/orchestrator`)

*   **Input**: User Message.
*   **Process**:
    1.  Fetch recent chat history.
    2.  LLM Decision: "Does this need a new sub-agent?", "Should I reactivate an existing sub-agent?", or "Can I answer directly?"
    3.  **If New Agent**: Create a `Task` document (`type: 'agent'`, `name: 'agent:researcher'`).
    4.  **If Reactivate**:
        *   Identify the existing `SubAgent` ID.
        *   Insert a new `user` message into `sub_agent_messages` with the user's follow-up request.
        *   Update `SubAgent` status to `active` (if it was completed).
        *   Update parent `Task` status to `running`.
    5.  **If No (Direct Answer)**: Reply directly (create `assistant` message).

## 2. Agent Initialization (`/webhook/agent-init`)

*   **Input**: New Task Document.
*   **Process**:
    1.  Create `SubAgent` document.
    2.  Update `Task` with `subAgentId`.
    3.  **Bootstrap**: Insert initial messages into `sub_agent_messages`:
        *   `system`: "You are a Researcher... **CRITICAL**: You MUST end every turn with either a Tool Call or a Final Answer. Do not just chat."
        *   `user`: Task instruction.

## 3. Agent Loop (`/webhook/agent-loop`)

*   **Input**: New Message (User or Tool).
*   **Process**:
    1.  **Pending Check (Optimization)**:
        *   **Why**: If a tool is currently running (status='pending'), calling the LLM again immediately is wasteful and can lead to race conditions or hallucinated "I'm waiting" responses.
        *   **Action**: Query `sub_agent_tool_calls` for any documents with `status: 'pending'`.
        *   **If found**: **STOP**. Do not call LLM. Wait for the pending tool to finish (it will trigger this loop again via `tool` message).
        *   **Note**: Ensure strong consistency when querying pending tools to avoid race conditions where two workers might miss each other's updates.
    2.  Fetch **ALL** messages for this `subAgentId`.
    3.  Call LLM (with tools).
    4.  **Outcome A (Tool Call)**:
        *   LLM wants to use a tool.
        *   Create `SubAgentToolCall` (pending).
        *   Create `assistant` message (with tool_calls).
    5.  **Outcome B (Final Answer)**:
        *   LLM provides text.
        *   Create `assistant` message (content).
        *   Update `SubAgent` status -> `completed`.
        *   Update parent `Task` status -> `success`, result = content.
    6.  **Outcome C (Ask User)**:
        *   LLM needs clarification from the user.
        *   Create `assistant` message (content = question).
        *   Update `SubAgent` status -> `waiting_for_user`.
        *   Update parent `Task` status -> `waiting`.

## 4. Tool Runner (`/webhook/tool-runner`)

*   **Input**: New Tool Call.
*   **Process**:
    1.  Execute Tool (Tavily, etc.).
    2.  Update `SubAgentToolCall` -> `success/failed`.
    3.  **Callback**: Create `tool` message in `sub_agent_messages` with the result.
        *   *Crucial*: This triggers the Agent Loop again.

## 5. Orchestrator Finalizer (`/webhook/orchestrator-finalize`)

*   **Input**: Completed Task.
*   **Process**:
    1.  Read Task Result.
    2.  Generate final response to user.
    3.  Create `assistant` message in main chat.

## 6. Orchestrator Notifier (`/webhook/orchestrator-notify`)

*   **Input**: Task (status='waiting').
*   **Process**:
    1.  Read the latest `assistant` message from the Sub-Agent (the question).
    2.  Create an `assistant` message in the **Main Chat** to notify the user.
        *   Content: "The Researcher needs more information: [Question]"
        *   Metadata: Link to the Task ID so the UI can highlight it.

## 7. Error Handling

*   **LLM Failures**: If the OpenAI API call fails (500/Timeout), the worker must not leave the agent in a "zombie" state.
    *   **Action**: Wrap LLM calls in `try/catch`.
    *   **Recovery**: Either retry (with backoff) or update `SubAgent` status to `failed` and insert a system error message into `sub_agent_messages` so the user is informed.

## 7. Discussion Points

1.  **Orchestrator Memory**: Does the Orchestrator need to see the *full* sub-agent history?
    *   *Decision*: No. It only sees the `Task.result`. This keeps the main context window clean.
2.  **Concurrency**: If multiple tools are called in parallel?
    *   The Loop handles this naturally. The LLM receives multiple tool results as multiple messages (or a sequence).
