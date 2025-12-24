# Design Documents

This directory now splits designs into server-side and SDK-focused documents.

## Server

- [server/000_requirements.md](server/000_requirements.md) - Requirements and constraints
- [server/001_architecture.md](server/001_architecture.md) - Initial architecture overview
- [server/002_storage_and_query.md](server/002_storage_and_query.md) - Storage and Query Engine design
- [server/003_restful_api.md](server/003_restful_api.md) - RESTful API design
- [server/004_replication.md](server/004_replication.md) - Replication protocol (server)
- [server/005_realtime_watching.md](server/005_realtime_watching.md) - Realtime watching mechanism
- [server/006_triggers.md](server/006_triggers.md) - Triggers system design
- [server/007_authentication.md](server/007_authentication.md) - Authentication design
- [server/008_authorization_rules.md](server/008_authorization_rules.md) - Authorization rules and logic
- [server/009_console.md](server/009_console.md) - Console/Dashboard design
- [server/010_control_plane.md](server/010_control_plane.md) - Control Plane design
- [server/011_transaction_implementation.md](server/011_transaction_implementation.md) - Transaction implementation details

## Automata

- [automata/000_design.md](automata/000_design.md) - The design of automata

## SDK

- [sdk/001_sdk_architecture.md](sdk/001_sdk_architecture.md) - SDK Architecture
- [sdk/002_replication_client.md](sdk/002_replication_client.md) - Replication client design (RxDB + replication/realtime)
- [sdk/003_authentication.md](sdk/003_authentication.md) - SDK authentication design
- [sdk/004_syntrix_client.md](sdk/004_syntrix_client.md) - SyntrixClient design (HTTP CRUD/query)
- [sdk/005_trigger_client.md](sdk/005_trigger_client.md) - TriggerClient design (trigger RPC + batch)
