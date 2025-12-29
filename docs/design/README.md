# Design Documents

This directory now splits designs into server-side and SDK-focused documents.

## Server

- [server/000_requirements.md](server/000_requirements.md) - Requirements and constraints
- [server/001_architecture.md](server/001_architecture.md) - Initial architecture overview
- [server/002_storage.md](server/002_storage.md) - Storage interfaces, backend abstraction, data model
- [server/003_query.md](server/003_query.md) - Query engine & DSL
- [server/004_restful_api.md](server/004_restful_api.md) - RESTful API design
- [server/005_replication.md](server/005_replication.md) - Replication protocol (server)
- [server/006_realtime_watching.md](server/006_realtime_watching.md) - Realtime watching mechanism
- [server/007_triggers.md](server/007_triggers.md) - Triggers system design
- [server/008_authentication.md](server/008_authentication.md) - Authentication design
- [server/009_authorization_rules.md](server/009_authorization_rules.md) - Authorization rules and logic
- [server/010_console.md](server/010_console.md) - Console/Dashboard design
- [server/011_control_plane.md](server/011_control_plane.md) - Control Plane design

## Monitor

- [monitor/000.requirements.md](monitor/000.requirements.md) - Monitoring and observability requirements
- [monitor/001.architecture.md](monitor/001.architecture.md) - Monitoring architecture and pipeline
- [monitor/002.pipeline_and_controls.md](monitor/002.pipeline_and_controls.md) - Collector pipeline, controls, schemas, and rollout

## Automata

- [automata/000_design.md](automata/000_design.md) - The design of automata

## SDK

- [sdk/001_sdk_architecture.md](sdk/001_sdk_architecture.md) - SDK Architecture
- [sdk/002_replication_client.md](sdk/002_replication_client.md) - Replication client design (RxDB + replication/realtime)
- [sdk/003_authentication.md](sdk/003_authentication.md) - SDK authentication design
- [sdk/004_syntrix_client.md](sdk/004_syntrix_client.md) - SyntrixClient design (HTTP CRUD/query)
- [sdk/005_trigger_client.md](sdk/005_trigger_client.md) - TriggerClient design (trigger RPC + batch)
