# Standalone Mode Design

**Date:** December 31, 2025
**Status:** Draft

## 1. Overview

Standalone mode packages all Syntrix services into a single process, replacing network communication with direct function calls. This simplifies deployment for development, testing, and small-scale production use cases.

## 2. Mode Comparison

| Aspect | Distributed Mode | Standalone Mode |
|--------|------------------|-----------------|
| **Process** | Multiple processes | Single process |
| **Communication** | HTTP/gRPC/NATS | Direct function calls |
| **Startup** | `syntrix --api`, `syntrix --query`, etc. | `syntrix --standalone` |
| **Latency** | Network RTT + serialization | Near-zero |
| **Scaling** | Horizontal (multiple instances) | Vertical only |
| **Fault Isolation** | Process-level | None (shared fate) |
| **Resource Usage** | Higher (multiple runtimes) | Lower (shared pools) |
| **Best For** | Production at scale | Dev/Test, Edge, Small deployments |

## 3. Architecture

### 3.1 Distributed Mode (Current)

```text
┌─────────────┐     HTTP      ┌─────────────┐     HTTP      ┌─────────────┐
│ API Gateway │ ──────────────> │Query Engine │ ──────────────> │     CSP     │
│   :8080     │               │    :8082    │               │    :8083    │
└─────────────┘               └─────────────┘               └─────────────┘
       │                             │                             │
       │                             │                             │
       ▼                             ▼                             ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                              MongoDB                                      │
└──────────────────────────────────────────────────────────────────────────┘

┌───────────────────┐    NATS    ┌───────────────────┐
│ Trigger Evaluator │ ◄──────────► │  Trigger Worker   │
└───────────────────┘            └───────────────────┘
```

### 3.2 Standalone Mode (Proposed)

```text
┌──────────────────────────────────────────────────────────────────────────┐
│                         Single Process                                    │
│  ┌─────────────┐                                                         │
│  │ API Gateway │──┐                                                      │
│  └─────────────┘  │                                                      │
│                   │  func call                                           │
│                   ▼                                                      │
│  ┌─────────────┐  │                                                      │
│  │Query Engine │◄─┘                                                      │
│  └─────────────┘                                                         │
│         │                                                                │
│         │  interface                                                     │
│         ▼                                                                │
│  ┌─────────────┐                                                         │
│  │ CSP Service │  (EmbeddedService impl, no HTTP)                        │
│  └─────────────┘                                                         │
│         │                                                                │
│         │                                                                │
│  ┌───────────────────┐    channel/     ┌───────────────────┐            │
│  │ Trigger Evaluator │ ◄─embedded NATS─► │  Trigger Worker   │            │
│  └───────────────────┘                 └───────────────────┘            │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                    Shared Storage Layer                           │   │
│  │                   (Single Connection Pool)                        │   │
│  └──────────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                              MongoDB                                      │
└──────────────────────────────────────────────────────────────────────────┘
```

## 4. Key Design Decisions

### 4.1 Interface-Based Abstraction

Existing service boundaries are converted to Go interfaces, allowing runtime selection between local and remote implementations.

**CSP Service Interface:**

```go
// internal/csp/interface.go
type Service interface {
    Watch(ctx context.Context, tenant, collection string) (<-chan storage.Event, error)
}

// EmbeddedService - direct storage access (standalone mode)
type EmbeddedService struct {
    storage storage.DocumentStore
}

// RemoteService - HTTP client (distributed mode)
type RemoteService struct {
    baseURL string
    client  *http.Client
}
```

**Query Engine Modification:**

```go
// internal/engine/internal/core/engine.go
type Engine struct {
    storage    storage.DocumentStore
    cspService csp.Service  // replaces: cspURL string
}

func (e *Engine) WatchCollection(ctx context.Context, tenant, collection string) (<-chan storage.Event, error) {
    return e.cspService.Watch(ctx, tenant, collection)  // interface call
}
```

### 4.2 Deployment Mode Selection

```go
// internal/services/options.go
type DeploymentMode int

const (
    ModeDistributed DeploymentMode = iota  // default, network communication
    ModeStandalone                          // all-in-one, direct calls
)

type Options struct {
    // existing...
    RunAPI              bool
    RunCSP              bool
    RunQuery            bool
    RunTriggerEvaluator bool
    RunTriggerWorker    bool
    
    // new
    Mode DeploymentMode
}
```

### 4.3 Service Manager Initialization

```go
// internal/services/manager_init.go
func (m *Manager) Init(ctx context.Context) error {
    if err := m.initStorage(ctx); err != nil {
        return err
    }
    
    // CSP service: local or remote based on mode
    var cspService csp.Service
    if m.opts.Mode == ModeStandalone {
        cspService = csp.NewEmbeddedService(m.docStore)
    } else {
        cspService = csp.NewRemoteService(m.cfg.Query.CSPServiceURL)
    }
    
    // Query service: always local engine in standalone, injected CSP
    queryService := engine.NewServiceWithCSP(m.docStore, cspService)
    
    // API server: direct injection (standalone) or HTTP client (distributed)
    if m.opts.Mode == ModeStandalone {
        m.initAPIServerDirect(queryService)
    } else if m.opts.RunAPI {
        m.initAPIServer(engine.NewClient(m.cfg.Gateway.QueryServiceURL))
    }
    
    // Trigger services
    if m.opts.RunTriggerEvaluator || m.opts.RunTriggerWorker {
        if err := m.initTriggerServices(); err != nil {
            return err
        }
    }
    
    return nil
}
```

### 4.4 Trigger Queue Strategy

| Option | Pros | Cons | Recommendation |
|--------|------|------|----------------|
| **Embedded NATS** | Full JetStream features, persistence | ~10MB binary size increase | **Default** |
| **Channel Queue** | Zero deps, minimal memory | No persistence, lost on crash | Dev/Test only |
| **External NATS** | Production-ready, shared infrastructure | Requires separate process | Large deployments |

**Embedded NATS Implementation:**

```go
// internal/services/embedded_nats.go
func (m *Manager) startEmbeddedNATS(ctx context.Context) (*nats.Conn, error) {
    opts := &server.Options{
        Host:      "127.0.0.1",
        Port:      -1,  // random available port
        JetStream: true,
        StoreDir:  filepath.Join(m.cfg.DataDir, "nats"),
        NoLog:     true,
    }
    
    ns, err := server.NewServer(opts)
    if err != nil {
        return nil, fmt.Errorf("create embedded nats: %w", err)
    }
    
    go ns.Start()
    if !ns.ReadyForConnections(5 * time.Second) {
        return nil, errors.New("nats server not ready")
    }
    
    return nats.Connect(ns.ClientURL())
}
```

## 5. Configuration

### 5.1 Command Line

```bash
# Standalone mode
syntrix --standalone

# Standalone with explicit components (for partial testing)
syntrix --standalone --no-trigger-worker

# Distributed mode (unchanged)
syntrix --api --query --csp
```

### 5.2 Configuration File

```yaml
# config.yml
deployment:
  mode: standalone  # "standalone" | "distributed"
  
  standalone:
    embedded_nats: true
    nats_data_dir: "./data/nats"
    
  # distributed settings remain unchanged
  distributed:
    query_service_url: "http://localhost:8082"
    csp_service_url: "http://localhost:8083"
```

## 6. Implementation Plan

### Phase 1: CSP Interface Abstraction

1. Create `internal/csp/interface.go` with `Service` interface
2. Implement `EmbeddedService` (direct storage call)
3. Extract existing HTTP logic to `RemoteService`
4. Modify `engine.Engine` to depend on `csp.Service` interface

**Files to modify:**
- `internal/csp/interface.go` (new)
- `internal/csp/embedded_service.go` (new)
- `internal/csp/remote_service.go` (new, extract from engine)
- `internal/engine/internal/core/engine.go`

### Phase 2: Service Manager Refactor

1. Add `DeploymentMode` to `Options`
2. Implement mode-based initialization logic
3. Add `--standalone` CLI flag
4. Skip HTTP server startup in standalone mode

**Files to modify:**
- `internal/services/options.go` (or inline in manager.go)
- `internal/services/manager_init.go`
- `internal/services/manager_start.go`
- `cmd/syntrix/main.go`

### Phase 3: Embedded NATS (Optional)

1. Add embedded NATS server support
2. Configure JetStream for standalone mode
3. Add configuration options

**Files to modify:**
- `internal/services/embedded_nats.go` (new)
- `internal/services/manager_init.go`
- `internal/config/config.go`

### Phase 4: Testing & Documentation

1. Add standalone mode integration tests
2. Update architecture documentation
3. Add deployment guide for standalone mode

## 7. Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Memory pressure | All components share heap | Monitor memory; allow disabling components |
| Panic propagation | One component crash kills all | `recover()` wrappers; structured error handling |
| Mixed logs | Harder to debug | Structured logging with component tags |
| Test coverage | Two code paths | Test matrix for both modes |

## 8. Success Criteria

- [ ] Single binary starts all services with `--standalone`
- [ ] No HTTP calls between internal services in standalone mode
- [ ] Existing distributed mode unchanged
- [ ] Integration tests pass for both modes
- [ ] Memory usage < 100MB baseline (no data)
- [ ] Startup time < 2s for standalone mode
