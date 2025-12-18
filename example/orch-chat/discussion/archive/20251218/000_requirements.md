# Chat App Requirements

## 1. Overview
This document defines the requirements for the `example/chat-app` demo, which showcases an Event-Driven LLM Agent architecture using Syntrix.

## 2. Core Requirements

### 2.1 Functional Requirements
1.  **Multi-Chat Support**: Users can create and switch between multiple chat sessions.
2.  **Realtime Messaging**: Messages appear instantly on the UI.
3.  **LLM Integration**:
    -   System must respond to user messages using an LLM (e.g., OpenAI).
    -   Support for "Thinking" state or intermediate status updates.
4.  **Tool Usage**:
    -   The Agent must be able to call external tools (specifically Tavily Search).
    -   Tool execution must be asynchronous and decoupled from the LLM inference.
5.  **Data Persistence**: All chats, messages, and tool execution states must be stored in Syntrix.

### 2.2 Technical Constraints
1.  **Architecture**: Must use Syntrix Triggers (Webhooks) to drive the logic. No direct client-to-LLM calls.
2.  **Frontend**: React + RxDB. Must use RxDB for all data interactions (read/write).
3.  **Backend**: Node.js (TypeScript) for the Trigger Worker.
4.  **Data Model**: Must use a Firestore-like hierarchical path structure.
5.  **User Identity**: Hardcoded `demo-user` is acceptable for this demo.

## 3. User Stories
-   **As a user**, I want to see a list of my past conversations so I can resume them.
-   **As a user**, I want to ask a question that requires current knowledge (e.g., "What is the weather in Tokyo?"), and see the agent search for it before answering.
-   **As a developer**, I want to see how Syntrix Triggers orchestrate the flow between the database, the LLM, and external tools.

## 4. Security Note
> **⚠️ Warning**: This demo application disables authentication for simplicity. In a production environment, you must:
> 1.  Enable JWT Authentication in Syntrix.
> 2.  Implement Authorization Rules (Security Rules) to restrict access to user data.
> 3.  Validate all inputs in the Trigger Worker.
