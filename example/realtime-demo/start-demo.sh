#!/bin/bash

# Realtime Demo Startup Script
# This script starts all required services for the realtime demo

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Parse command line arguments
RESTART_MODE=false
while [[ $# -gt 0 ]]; do
    case $1 in
        --restart)
            RESTART_MODE=true
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: $0 [--restart]"
            echo "  --restart  Stop and restart Syntrix server even if already running"
            exit 1
            ;;
    esac
done

echo "=== Realtime Demo Startup Script ==="
echo "Project root: $PROJECT_ROOT"
if [ "$RESTART_MODE" = true ]; then
    echo "Mode: Restart enabled"
fi
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to check if a command exists
check_command() {
    if ! command -v "$1" &> /dev/null; then
        echo -e "${RED}Error: $1 is not installed${NC}"
        return 1
    fi
    return 0
}

# Function to check if a port is in use
check_port() {
    if lsof -i:"$1" &> /dev/null; then
        return 0  # Port is in use
    fi
    return 1  # Port is free
}

# Function to wait for a service to be ready
wait_for_service() {
    local host=$1
    local port=$2
    local name=$3
    local max_attempts=30
    local attempt=1

    echo -n "Waiting for $name to be ready..."
    while ! nc -z "$host" "$port" 2>/dev/null; do
        if [ $attempt -ge $max_attempts ]; then
            echo -e " ${RED}TIMEOUT${NC}"
            return 1
        fi
        sleep 1
        attempt=$((attempt + 1))
        echo -n "."
    done
    echo -e " ${GREEN}OK${NC}"
    return 0
}

# Function to stop syntrix server
stop_syntrix() {
    echo "Stopping existing Syntrix server..."
    local pids=$(pgrep -f "bin/syntrix" 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "$pids" | xargs kill 2>/dev/null || true
        sleep 2
        # Force kill if still running
        pids=$(pgrep -f "bin/syntrix" 2>/dev/null || true)
        if [ -n "$pids" ]; then
            echo "$pids" | xargs kill -9 2>/dev/null || true
        fi
        echo -e "${GREEN}Syntrix server stopped${NC}"
    else
        echo -e "${YELLOW}No Syntrix server process found${NC}"
    fi
}

# Check prerequisites
echo "Checking prerequisites..."
check_command docker || exit 1
check_command go || exit 1
check_command bun || exit 1
echo -e "${GREEN}All prerequisites satisfied${NC}"
echo ""

# Step 1: Start Docker services (MongoDB & NATS)
echo "=== Step 1: Starting Docker Services ==="
cd "$PROJECT_ROOT"

if check_port 27017; then
    echo -e "${YELLOW}MongoDB already running on port 27017${NC}"
else
    echo "Starting MongoDB and NATS..."
    docker compose -f .devcontainer/docker-compose.yml up -d
    wait_for_service localhost 27017 "MongoDB"
fi

if check_port 4222; then
    echo -e "${YELLOW}NATS already running on port 4222${NC}"
else
    wait_for_service localhost 4222 "NATS"
fi
echo ""

# Step 2: Build and start Syntrix server
echo "=== Step 2: Building Syntrix Server ==="
cd "$PROJECT_ROOT"
make build
echo ""

echo "=== Step 3: Starting Syntrix Server ==="
if check_port 8080; then
    if [ "$RESTART_MODE" = true ]; then
        stop_syntrix
        sleep 1
        echo "Starting Syntrix server (binding to 0.0.0.0 for LAN access)..."
        "$PROJECT_ROOT/bin/syntrix" --all --host 0.0.0.0 &
        SYNTRIX_PID=$!
        echo "Syntrix server started with PID: $SYNTRIX_PID"
        wait_for_service localhost 8080 "Syntrix API"
    else
        echo -e "${YELLOW}Port 8080 is already in use. Syntrix server may already be running.${NC}"
        echo "Use --restart flag to stop and restart the server."
    fi
else
    echo "Starting Syntrix server (binding to 0.0.0.0 for LAN access)..."
    "$PROJECT_ROOT/bin/syntrix" --all --host 0.0.0.0 &
    SYNTRIX_PID=$!
    echo "Syntrix server started with PID: $SYNTRIX_PID"
    wait_for_service localhost 8080 "Syntrix API"
fi
echo ""

# Step 3: Install demo dependencies
echo "=== Step 4: Installing Demo Dependencies ==="
cd "$SCRIPT_DIR"
if [ ! -d "node_modules" ]; then
    echo "Installing dependencies..."
    bun install
else
    echo -e "${YELLOW}Dependencies already installed${NC}"
fi
echo ""

# Step 4: Start demo server
echo "=== Step 5: Starting Demo Server ==="
if check_port 3000; then
    echo "Stopping existing demo server on port 3000..."
    # Kill process using port 3000
    local pids=$(lsof -ti:3000 2>/dev/null || true)
    if [ -n "$pids" ]; then
        echo "$pids" | xargs kill 2>/dev/null || true
        sleep 1
    fi
fi
echo "Starting demo server..."
bun run dev &
DEMO_PID=$!
echo "Demo server started with PID: $DEMO_PID"
sleep 2
echo ""

# Print access information
echo "=== Demo Ready ==="
echo -e "${GREEN}All services started successfully!${NC}"
echo ""
echo "Access the demo at:"
echo "  - Local:    http://localhost:3000/"

# Get local IP for LAN access hint
LOCAL_IP=$(ip route get 1 2>/dev/null | awk '{print $7; exit}' || hostname -I 2>/dev/null | awk '{print $1}' || ifconfig 2>/dev/null | grep -Eo 'inet (addr:)?([0-9]*\.){3}[0-9]*' | grep -v '127.0.0.1' | head -1 | awk '{print $2}')
if [ -n "$LOCAL_IP" ]; then
    echo "  - Network:  http://$LOCAL_IP:3000/"
fi
echo ""
echo "Press Ctrl+C to stop all services"

# Wait for interrupt
wait
