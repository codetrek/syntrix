# Realtime Demo

A simple demo showcasing Syntrix TypeScript SDK's realtime WebSocket synchronization.

## Features

- Two clients connecting to the same Syntrix server
- Real-time message synchronization via WebSocket
- Auto-login and subscription
- Event logging

## Prerequisites

- Docker & Docker Compose (for MongoDB and NATS)
- Go 1.21+ (for building Syntrix server)
- Bun (for running the demo)

## Quick Start

### One-Click Start (Recommended)

```bash
cd example/realtime-demo
./start-demo.sh

# Or with restart flag (stops existing Syntrix server and restarts)
./start-demo.sh --restart
```

This script will automatically:
1. Start Docker services (MongoDB & NATS)
2. Build the Syntrix server
3. Start Syntrix server with LAN access enabled (`--host 0.0.0.0`)
4. Install demo dependencies
5. Start the demo server

### Manual Start

#### Step 1: Start Infrastructure (MongoDB & NATS)

```bash
cd /path/to/syntrix
docker compose up -d
```

### Step 2: Build and Start Syntrix Server

```bash
# Build the server
make build

# Start with default settings (localhost only)
make run

# OR start with LAN access enabled (required for accessing from other devices)
./bin/syntrix --all --host 0.0.0.0
```

### Step 3: Install Demo Dependencies

```bash
cd example/realtime-demo
bun install
```

### Step 4: Start the Demo

```bash
bun run dev
```

### Step 5: Access the Demo

- **Local access**: http://localhost:3000/
- **LAN access**: http://\<your-ip\>:3000/ (requires `--host 0.0.0.0` on server)

Click **"Quick Connect Both"** to connect two clients simultaneously.

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
