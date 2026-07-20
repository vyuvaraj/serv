# ServCron Roadmap

This roadmap outlines the planned development phases for the ServCron distributed scheduling service.

---

## Differentiating Factors (Why ServCron?)

* **Language-Native Synchronization**: Designed to coordinate Serv-lang's declarative `every` and `cron` syntax code blocks across multiple replicated microservice pods.
* **Resilient Lease Lock Election**: Built-in lease-based leader election using Redis or local standalones, guaranteeing exactly-once scheduling without Consul/ZooKeeper sidecar overhead.
* **OTel Trace Correlation**: Standard header propagation (`traceparent`) automatically hooks job triggers into OTel context, allowing operations to trace execution duration and failures in `ServConsole`.
* **Zero-config REST Trigger Control**: Dynamic REST APIs allow viewing, pausing, deleting, or forcing instantaneous executions of any scheduled job for testability.

---

## Phase 1: Core Scheduling & Distributed Locks (Completed)
- [x] **Interval Execution Engine**: Scheduling loop for duration-based periodic tasks.
- [x] **Redis Lease Leader Election**: Lock-based leader promotion to avoid duplicate runs across instances.
- [x] **REST APIs**: Full CRUD operations on jobs + manual run triggers.
- [x] **OTel Tracing Integration**: Spans emitted on task execution.
- [x] **GitHub Actions CI Pipeline**: Automated build and test pipeline configuration.

## Phase 2: Cron Pattern Parsing (Q3 2026)
- [x] **Standard Cron Syntax**: Integrate native parsing for 5-field cron specifications (e.g. `0 9 * * 1-5` for weekdays at 9 AM).
- [x] **Dynamic Load Balancing**: Distribute different jobs across different nodes rather than executing all jobs on a single leader.

## Phase 3: ServStore Backing (Q4 2026)
- [x] **Persistent Job Registry**: Back job state persistence to a `ServStore` S3 bucket, preventing scheduled task losses during server crashes or restarts.
- [x] **Job Execution Audit History**: Logs execution logs, status results, and metrics directly to storage.


## Phase 4: Workflow Orchestration (Next Level)
- [ ] **DAG-based Job Pipelines**: Define multi-step workflows with dependencies (`job A → job B → job C`). Fan-out/fan-in support.
- [ ] **Job Chaining via ServQueue**: Trigger next job by publishing to ServQueue topic on completion. Event-driven scheduling.
- [ ] **Retry Policies & Dead Letter**: Configurable retry count with exponential backoff. Failed jobs route to DLQ topic.
- [ ] **Time Zone Awareness**: Schedule jobs in specific timezones (not just UTC). Critical for business-hour triggers.
- [ ] **Job Templating**: Parameterized job definitions that accept runtime variables (e.g., date ranges, partition IDs).
- [ ] **Execution Resource Limits**: CPU/memory limits per job execution. Kill long-running jobs after timeout.
- [ ] **Cron Expression Builder UI**: Visual cron builder in ServConsole with next-5-runs preview.

> **Note:** For long-running, stateful, multi-day workflows with human approval gates and saga compensation, see **ServFlow** — the proposed workflow orchestrator. ServCron Phase 4 focuses on short-lived DAG job pipelines (minutes), while ServFlow handles durable workflows (hours/days) with state checkpointing. They integrate via ServQueue: ServCron can trigger ServFlow workflows on schedule, and ServFlow steps can schedule follow-up jobs in ServCron.

## Phase 5: Architectural Depth & DevOps (Pending)
- [ ] **`serv cron list` CLI** — Terminal command showing next 5 scheduled runs per job, last outcome, and failure count — invaluable for on-call debugging (DevOps)
- [ ] **Job Run Dry-Mode** — `serv cron run --dry-run <job>` executes a job with verbose logging but without side effects; uses request mocking for HTTP tasks (DX)
- [ ] **Structured Job Output Logs** — Persist structured JSON stdout/stderr per run to ServStore with trace_id linkage; surface in ServConsole and searchable via ServTrace (DX / Observability)

> See [UNIFIED_ROADMAP.md](../UNIFIED_ROADMAP.md) for the full ecosystem priority matrix and architectural recommendations.
