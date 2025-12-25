# Realtime Demo

A simple demo showcasing Syntrix TypeScript SDK's realtime WebSocket synchronization.

## Features

- Two clients connecting to the same Syntrix server
- Real-time message synchronization via WebSocket
- Auto-login and subscription
- Event logging

## Prerequisites

- Syntrix server running on port 8080
- Bun installed

## Quick Start

```bash
# Terminal 1: Start Syntrix server
cd /path/to/syntrix
make run

# Terminal 2: Start demo (builds SDK bundle automatically)
cd example/realtime-demo
bun run dev
```

Then open http://localhost:3000/

For LAN access: http://<your-ip>:3000/

## How It Works

1. **TypeScript Source**: Application code is in `src/main.ts`
2. **SDK Dependency**: Uses `@syntrix/client` via local file reference
3. **Build**: `bun run dev` compiles TypeScript to `dist/main.js`
4. **Static Server**: `serve` hosts the HTML and compiled JS on port 3000
5. **WebSocket**: Each client connects to `ws://<host>:8080/realtime/ws` for real-time updates
6. **Dynamic Host**: The demo automatically uses the current page's hostname for API connections

## Files

- `index.html` - Demo UI with two chat clients
- `src/main.ts` - TypeScript application code
- `dist/main.js` - Built bundle (generated, gitignored)
- `package.json` - Project configuration
