# Design Documents

This directory now splits designs into server-side and SDK-focused documents.

## Server
- [server/000_requirements.md](server/000_requirements.md) - Requirements and constraints
- [server/001_initial_architecture.md](server/001_initial_architecture.md) - Initial architecture overview
- [server/002_storage_and_query_engine.md](server/002_storage_and_query_engine.md) - Storage and Query Engine design
- [server/003_api_and_realtime.md](server/003_api_and_realtime.md) - API and Realtime features
- [server/003_01_replication.md](server/003_01_replication.md) - Replication protocol (server)
- [server/004_authorization_rules.md](server/004_authorization_rules.md) - Authorization rules and logic
- [server/005_realtime_watching.md](server/005_realtime_watching.md) - Realtime watching mechanism
- [server/006_triggers.md](server/006_triggers.md) - Triggers system design
- [server/007_identity.md](server/007_identity.md) - Identity management
- [server/008_console.md](server/008_console.md) - Console/Dashboard design
- [server/010_control_plane.md](server/010_control_plane.md) - Control Plane design
- [server/011_transaction_implementation.md](server/011_transaction_implementation.md) - Transaction implementation details

## SDK
- [sdk/001_sdk_architecture.md](sdk/001_sdk_architecture.md) - SDK Architecture
- [sdk/002_replication_client.md](sdk/002_replication_client.md) - Replication client design (RxDB + replication/realtime)
- [sdk/003_authentication.md](sdk/003_authentication.md) - SDK authentication design
- [sdk/004_syntrix_client.md](sdk/004_syntrix_client.md) - SyntrixClient design (HTTP CRUD/query)
- [sdk/005_trigger_client.md](sdk/005_trigger_client.md) - TriggerClient design (trigger RPC + batch)
