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

## Phase 4: Architectural Depth (Completed)
- [x] **Durable Sagas State Machine** — Durable execution rollback engine backed by ServStore (CORE.2) [July 1, 2026]

## Phase 5: Event-Driven Sagas (Completed)
- [x] **Event-Driven Sagas Orchestration** — Asynchronous compensations triggered on STOMP topic events (CORE.3) [July 1, 2026]

## Phase 6: Productization & Visual Editing (Pending)
- [ ] **Interactive Visual Workflow Designer** — Drag-and-drop stateful workflow editor generating native `serv-lang` code (UI.4)
- [ ] **Time-Travel Workflow Replay** — Debug complex workflow errors by replaying trace logs step-by-step locally (DX.13)

## Phase 7: Package Extraction & Reliability (Pending — July 2026)

> **Issues:** `main.go` is 803 lines. 20+ `.state` files committed to repo. No `pkg/` structure.

| # | Item | Effort | Description | Status |
|---|------|--------|-------------|--------|
| 7.1 | **Clean `.state` files from repo** | Small | Add `*.state` to `.gitignore`, remove committed state files from git history | [ ] |
| 7.2 | **Extract `pkg/engine/`** | Medium | Move DAG execution engine, step scheduling, and dependency resolution into dedicated package | [ ] |
| 7.3 | **Extract `pkg/saga/`** | Medium | Move compensation/rollback logic and durable saga state machine into dedicated package | [ ] |
| 7.4 | **Extract `pkg/api/`** | Small | Move HTTP handlers and approval gate endpoints into dedicated package | [ ] |
| 7.5 | **Workflow versioning** | Medium | Support multiple active versions of a workflow definition. New instances use latest; running instances continue on their version | [ ] |
| 7.6 | **Conditional branching** | Medium | Support `if/else` step routing based on output of previous step. Dynamic path selection in DAG | [ ] |
| 7.7 | **Sub-workflow invocation** | Medium | Call another workflow as a step. Enables composition and reuse of workflow patterns | [ ] |
| 7.8 | **Parallel step execution** | Small | Execute independent steps concurrently (fan-out) and join results before dependent steps | [ ] |

> See [UNIFIED_ROADMAP.md](../servverse-repo/UNIFIED_ROADMAP.md) for the full ecosystem priority matrix.


---

## Phase 8: Test Coverage (Pending — Phase 22)

> **Issue:** Only 11 test functions in 1 file.

| # | Item | Effort | Description | Status |
|---|------|--------|-------------|--------|
| 8.1 | **Expand test suite** | Medium | From 11 → 35+ test functions: DAG cycle detection, concurrent approval + timeout race, saga compensation ordering, checkpoint corruption recovery, sub-workflow invocation | [ ] |
