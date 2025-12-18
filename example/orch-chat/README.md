# Syntrix Chat App Demo

This example demonstrates an **Event-Driven LLM Agent** architecture using Syntrix. It showcases how to build a reactive chat application where business logic (LLM inference, tool execution) is decoupled from the frontend and handled by backend workers triggered by database events.

## Architecture

- **Frontend**: React + RxDB (Syncs data from Syntrix, no direct API calls to LLM).
- **Database**: Syntrix (Stores messages, tool calls, and triggers events).
- **Backend Worker**: Node.js (Listens to Syntrix Webhooks, calls Azure OpenAI / Tavily).

## Prerequisites

- **Go 1.23+** (to run Syntrix)
- **Bun** or **Node.js** (to run the worker and frontend)
- **MongoDB** (running locally or accessible)
- **Azure OpenAI API Key** (or standard OpenAI Key)
- **Tavily API Key** (for search functionality)

## Step-by-Step Run Guide

You will need **3 separate terminal windows** to run the full stack.

### Terminal 1: Start Syntrix Server

1.  Navigate to the root `syntrix` directory.
2.  Ensure your configuration points to the triggers file.
    *   Edit `config.yml` (or `config.local.yml`):
        ```yaml
        trigger:
          rules_file: "example/orch-chat/triggers.json"
          # ... other config
        ```
3.  Build and run Syntrix:
    ```bash
    make build
    ./bin/syntrix --all
    ```
    *   Syntrix API should be running at `http://localhost:8080`.
    *   Realtime API should be running at `http://localhost:8081`.

### Terminal 2: Start Trigger Worker

This worker handles the "brain" of the agent.

1.  Navigate to the worker directory:
    ```bash
    cd example/chat-app/trigger-worker
    ```
2.  Install dependencies:
    ```bash
    bun install
    ```
3.  Configure Environment:
    *   Copy `.env.example` to `.env` (if you haven't already).
    *   Fill in your API keys:
        ```env
        PORT=8000
        SYNTRIX_API_URL=http://localhost:8080
        AZURE_OPENAI_ENDPOINT=https://your-resource.openai.azure.com/
        AZURE_OPENAI_API_KEY=your_key
        AZURE_OPENAI_MODEL=gpt-4o
        TAVILY_API_KEY=your_tavily_key
        ```
4.  Start the worker:
    ```bash
    bun run dev
    ```
    *   Worker should be listening at `http://localhost:8000`.

### Terminal 3: Start Frontend App

1.  Navigate to the app directory:
    ```bash
    cd example/chat-app
    ```
2.  Install dependencies:
    ```bash
    bun install
    ```
3.  Start the development server:
    ```bash
    bun run dev
    ```
4.  Open your browser at `http://localhost:5173`.

## How to Use

1.  **Create a Chat**: Click the "+ New Chat" button in the sidebar.
2.  **Send a Message**: Type "Hello" to test the LLM response.
3.  **Test Tools**: Type a query that requires internet access, e.g., *"What is the current weather in Tokyo?"*.
    *   **Observe**:
        1.  The message appears immediately (Optimistic UI).
        2.  Syntrix triggers the worker.
        3.  Worker sees the intent and creates a `toolcall` document (Status: Pending).
        4.  Worker picks up the pending tool call, executes Tavily search, and updates the document (Status: Success).
        5.  Worker picks up the success event, feeds the result back to LLM.
        6.  LLM generates the final answer.
        7.  Frontend syncs the new message and displays it.

## Troubleshooting

-   **Worker Errors**: Check Terminal 2 logs. If you see "Missing credentials", check your `.env` file.
-   **No Response**:
    -   Check Terminal 1 (Syntrix) logs to see if the Trigger was fired and if the Webhook delivery succeeded (HTTP 200).
    -   Check if `triggers.json` is correctly loaded by Syntrix.
-   **Frontend Sync Issues**: Open Browser Console (F12). Check for WebSocket connection errors or RxDB replication errors.
