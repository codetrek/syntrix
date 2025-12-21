# Client SDK Architecture Discussion (Placeholder)

**Date:** December 14, 2025
**Topic:** Native SDK Design and Synchronization Protocol

## Topics to Discuss

### 1. Native SDK Architecture (Go, Mobile, etc.)
- **Beyond RxDB**: Designing for platforms where RxDB isn't available.
- **Local Storage**: Abstraction layer for local caching (SQLite, LevelDB, etc.).
- **State Management**: Handling optimistic UI updates and rollbacks.

### 2. Synchronization Protocol Details
- **Offline-First Logic**: Detailed state machine for connection status.
- **Resume Tokens**: Handling disconnects and ensuring no data loss during sync.
- **Conflict Resolution**: Client-side vs Server-side strategies.

### 3. Change Stream Reliability (Client Side)
- **Ack Mechanisms**: Ensuring clients acknowledge receipt of messages.
