# ServFlow Roadmap

This roadmap outlines the planned development phases for the ServFlow workflow orchestrator.

---

## Phase 1: Core DAG Orchestrator (In Progress)
- [x] **DAG-based workflow definitions** — Step execution, dependencies, and task runner. [June 29, 2026]
- [x] **Durable execution** — State checkpointing and mid-run restart survivability. [June 29, 2026]
- [x] **Compensation / rollback** — Automatic saga failure reversal execution. [June 29, 2026]
- [x] **Event-triggered workflows** — Trigger execution via ServQueue. [June 29, 2026]
- [x] **Serv-lang integration** — Native workflow engine syntax support. [June 29, 2026]

## Phase 2: Workflow Management
- [x] **Human approval gates** — Execution pauses pending manual approval. [June 29, 2026]
- [x] **Timeout & retry policies** — Configurable step timeouts and retry strategies. [June 29, 2026]
- [x] **Execution history** — Complete audit trail logs and replay support. [June 29, 2026]
- [x] **Visual workflow editor** — Backend DAG validation and Mermaid visualization APIs. [June 29, 2026]

## Phase 3: Production Hardening & Resilience (Completed)
- [x] **State-Isolated Unit & Validation Tests** — Table-driven checks for validation of workflow schemas. [June 30, 2026]
- [x] **Interface Abstractions & Decoupled Storage** — Extract workflow definition and checkpoints behind `WorkflowStore` interface. [June 30, 2026]
- [x] **Structured Logging & OTel Tracing** — Add TraceMiddleware for tracing context propagation and JSON log format. [June 30, 2026]
- [x] **SIGTERM Graceful Shutdown** — Register listener to shut down HTTP listener cleanly with a 5-second timeout. [June 30, 2026]

## Phase 4: Architectural Depth (Pending)
- [ ] **Durable Sagas State Machine** — Durable execution rollback engine backed by ServStore (CORE.2)
