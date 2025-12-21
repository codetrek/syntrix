# Syntrix

Syntrix is a Firestore-like realtime document database built with Go and MongoDB.

## Project Structure

```
syntrix/
├── cmd/
│   ├── syntrix/          # All-in-One Service: API Gateway, Realtime Gateway, Query Engine, Change Stream Processor
│   ├── syntrix-cli/      # Syntrix command line
├── internal/             # Private application code
│   ├── api/              # API Handlers and Router
│   ├── config/           # Configuration loading
│   ├── csp/              # Change Stream Processor Logic
│   ├── query/            # Query Engine and Internal Server
│   ├── realtime/         # WebSocket Hub and Client
│   ├── services/         # Service launcher
│   └── storage/          # Storage interfaces and MongoDB backend
├── pkg/                  # Public library code
├── tests/                # Integration tests
├── docs/design/          # Architecture and Design documents
├── go.mod                # Go module definition
└── README.md
```

## Getting Started

### Prerequisites

- Go 1.23 or later
- MongoDB 4.0+ (Replica Set required for Realtime features)

### Build

```bash
make build
```

### Run All-in-One Service (Recommended)

```bash
make run
# or
./bin/syntrix --all
```

This runs all services (API, Realtime, Query, CSP) in a single process.

### Run Individual Services

#### API Service

```bash
./bin/syntrix --api
```

The API listens on port 8080 by default.

#### Realtime Service

```bash
./bin/syntrix --realtime
```

The Realtime service listens on port 8081 by default.
**Note:** The Realtime service requires MongoDB to be running as a **Replica Set** to use Change Streams. If running against a standalone instance, it will fail to start watching.

#### Query Service (Internal)

```bash
./bin/syntrix --query
```

The Query service listens on port 8082 by default. This service exposes the internal Query Engine API.

#### Change Stream Processor Service

```bash
./bin/syntrix --csp
```

The Change Stream Processor service listens on port 8083 by default. It handles background processing of database change streams.

### Configuration

Configuration is loaded in the following order:
1. Defaults
2. `config.yml`
3. `config.local.yml` (gitignored)
4. Environment Variables (`MONGO_URI`, `DB_NAME`, `API_PORT`, `REALTIME_PORT`, `QUERY_PORT`, `CSP_PORT`)

### Testing

Run unit tests:
```bash
make test
```

Run integration tests (requires running MongoDB):
```bash
go test ./tests/integration/... -v
```

## Features

- **Document Storage**: CRUD operations with Optimistic Locking (CAS).
- **Query Engine**: Filter and Sort documents.
- **Realtime Updates**: WebSocket-based realtime events (Create, Update, Delete).
- **Change Stream Processor**: Background processing of database change streams.
- **Architecture**: Splittable Monolith (API, Realtime, and CSP services can run independently or together).
