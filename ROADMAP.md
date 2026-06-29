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
- [ ] **Execution history** — Complete audit trail logs.
