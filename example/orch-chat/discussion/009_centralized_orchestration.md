# Design: Centralized Orchestration & Sub-Agent Interaction

## 1. Core Philosophy
- **Single Entry Point**: Users only interact with the Orchestrator via the main chat window (`messages` collection).
- **Orchestrator as Proxy**: Sub-Agents do not interact directly with the user. They request input, and the Orchestrator provides it (either from context or by relaying user input).
- **Strict Data Flow**:
  - Frontend ONLY writes to `users/*/orch-chats/*/messages` (and creates chats).
  - All other collections (`tasks`, `sub_agents`, `sub_agent_messages`) are managed exclusively by Backend Triggers/Workers.

## 2. Workflow & Scenario Deduction

### Phase 1: Sub-Agent Requests Input
1. **Sub-Agent** (in `agent-loop`) determines it needs user input.
2. Calls tool `ask_user(question)`.
3. **Handler** updates:
   - Sub-Agent Status -> `waiting_for_user`
   - Task Status -> `waiting`
   - Adds `assistant` message with question to `sub_agent_messages`.
4. **Trigger** `on-task-waiting` fires `orchestrator-notify`.
5. **Notify Handler** posts a message to the **Main Chat**:
   - Role: `assistant` (or system proxy)
   - Content: "Agent [Name] needs input: [Question]"

### Phase 2: Orchestrator Intervention (Scenario Analysis)

#### Scenario A: User Responds (Manual Input)
1. **User** types answer in Main Chat (e.g., "It is 2024").
2. **Frontend** inserts message into `messages` collection.
3. **Trigger** `on-user-message` fires `orchestrator`.
4. **Orchestrator** detects waiting agent and matches user input to the question.
5. **Orchestrator** calls `reply_to_agent(subAgentId, "It is 2024")`.

#### Scenario B: Orchestrator Decides (Context Awareness)
*Note: This requires the Orchestrator to be triggered or to check context before asking the user, or upon receiving a user message that implies the answer.*
1. **Sub-Agent** asks: "What is the user's name?"
2. **Orchestrator** (triggered by user message or system event) checks history.
3. **Orchestrator** finds user name "Alice" in previous context.
4. **Orchestrator** decides **NOT** to ask user.
5. **Orchestrator** directly calls `reply_to_agent(subAgentId, "Alice")`.

### Phase 3: Sub-Agent Resumption
1. **Tool Runner** (for `reply_to_agent`):
   - Inserts `user` message into `sub_agent_messages` with the content provided by Orchestrator.
   - Updates Sub-Agent Status -> `active`.
   - Updates Task Status -> `running`.
2. **Trigger** `on-sub-agent-message` fires `agent-loop`.
3. Sub-Agent continues execution.

## 3. Implementation Plan

### Backend (Trigger Worker)
1. **`orchestrator.ts`**:
   - Add `reply_to_agent` tool definition.
   - Update logic to fetch `waiting` sub-agents.
   - Update System Prompt to handle routing.
   - Implement `reply_to_agent` logic (write to sub-agent messages, update status).

### Frontend (Chat App)
1. **`SubAgentInspector.tsx`**:
   - Remove `MessageInput` and `customSubmit` logic.
   - Make the view Read-Only.
   - (Optional) Add a visual indicator that the agent is waiting for input in the main chat.
2. **`TaskBlock.tsx`**:
   - Update "Respond" button to simply focus the main chat input or show a toast "Please reply in the main chat".

## 4. Benefits
- **Security**: Frontend write permissions are strictly scoped.
- **Context Awareness**: Orchestrator can potentially answer questions automatically if it has the context, reducing user friction.
- **Consistency**: All state transitions are managed by the backend.

## 5. Design Rationale

Why choose **Centralized Orchestration** over direct Sub-Agent interaction?

1.  **Unified User Experience (UX)**:
    - Users shouldn't need to manage multiple chat windows or "contexts". They talk to one "Assistant" (the Orchestrator).
    - If a background task needs input, it should appear in the main flow, just like a colleague asking a question in a main channel.

2.  **Contextual Intelligence**:
    - Sub-Agents are narrow-scoped workers. They don't know what the user said 5 minutes ago in the main chat.
    - The Orchestrator holds the **Global Context**. It can answer Sub-Agent questions using information the user has already provided, preventing repetitive questioning.

3.  **Security & Architecture**:
    - **Single Write Path**: Restricting frontend writes to `messages` simplifies permission rules (ACLs). We don't need to grant users write access to deep, dynamic sub-collections.
    - **Backend State Management**: State transitions (Waiting -> Active) are complex. Managing them in the backend (Orchestrator) ensures consistency and prevents race conditions that might occur if the frontend manipulated state directly.

## 6. Critical Review & Refinements

### Issue 1: Passive vs. Proactive Orchestration
In the initial design, **Scenario B (Auto-Answer)** was vague about *when* the Orchestrator checks for answers. If `on-task-waiting` only triggers a dumb notification, the Orchestrator misses the chance to answer automatically *before* bothering the user.

**Refined Workflow for Phase 1:**
1.  **Trigger** `on-task-waiting` fires `orchestrator-notify`.
2.  **Handler Upgrade**: `orchestrator-notify` should not just post a message. It should:
    -   **Step 1**: Fetch Chat History.
    -   **Step 2**: Call LLM (Orchestrator Persona) with: "Agent X is asking: [Question]. Can you answer this based on history? If yes, call `reply_to_agent`. If no, call `ask_user`."
    -   **Step 3**:
        -   If `reply_to_agent` called: Done. (User sees nothing, or maybe a "Agent X checked info..." log).
        -   If `ask_user` called: Post the message to Main Chat.

### Issue 2: User Feedback Loop
When the user replies "It is 2024", and the Orchestrator forwards this to the agent, the Main Chat might look like:
- *User*: "It is 2024"
- *(Silence)*
- *(Later)* "Agent Researcher Completed: ..."

The user might wonder if the input was received.
**Refinement**: The Orchestrator should optionally reply to the user: "Thanks, I've updated the researcher." or use a UI reaction if possible. For now, a brief text acknowledgement is safer.

### Issue 3: Infinite Loops
If the Orchestrator confidently provides a *wrong* answer, the Sub-Agent might fail and ask again.
**Refinement**: The `reply_to_agent` tool should include a `confidence` score or the Sub-Agent should have a "retry limit" for the same question.

---
*End of Document*
