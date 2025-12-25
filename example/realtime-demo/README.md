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

# Terminal 2: Build SDK and start demo server
cd /path/to/syntrix
cd sdk/syntrix-client-ts && bun run build && cd ../..
bunx serve -l 3000 -C
```

Then open http://localhost:3000/example/realtime-demo/

For LAN access: http://<your-ip>:3000/example/realtime-demo/

## How It Works

1. **SDK Bundle**: The SDK is built to `sdk/syntrix-client-ts/dist/syntrix-browser.js`
2. **Static Server**: `serve` runs from project root to allow cross-directory file access
3. **WebSocket**: Each client connects to `ws://<host>:8080/realtime/ws` for real-time updates
4. **Dynamic Host**: The demo automatically uses the current page's hostname for API connections

## Files

- `index.html` - Demo UI with two chat clients
- `package.json` - Project configuration (references SDK via relative path)
