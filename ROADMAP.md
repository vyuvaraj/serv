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

## Phase 2: Cron Pattern Parsing (Q3 2026)
- [ ] **Standard Cron Syntax**: Integrate native parsing for 5-field cron specifications (e.g. `0 9 * * 1-5` for weekdays at 9 AM).
- [ ] **Dynamic Load Balancing**: Distribute different jobs across different nodes rather than executing all jobs on a single leader.

## Phase 3: ServStore Backing (Q4 2026)
- [ ] **Persistent Job Registry**: Back job state persistence to a `ServStore` S3 bucket, preventing scheduled task losses during server crashes or restarts.
- [ ] **Job Execution Audit History**: Logs execution logs, status results, and metrics directly to storage.
