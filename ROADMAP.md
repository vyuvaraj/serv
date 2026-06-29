# ServTrace Roadmap

This roadmap outlines the planned development phases for the ServTrace distributed tracing backend.

---

## Differentiating Factors (Why ServTrace vs. Jaeger/Tempo/Zipkin?)
* **Compiler-Linked Stacktraces**: Natively maps trace spans to `.srv` source code file paths and line numbers instead of generated Go binaries.
* **Zero-Agent Collection**: Directly ingests OTLP/HTTP payloads exported by `ServShared` out-of-the-box, removing the need for Jaeger sidecars or Otel collector daemons.
* **Extremely Lightweight**: Single static Go binary utilizing a circular memory buffer, making it perfect for rapid local debugging.

---

## Phase 1: OTLP Ingest & Tree Collector (Completed)
- [x] **OTLP Ingestion endpoint**: Supports standard `/v1/traces` HTTP POST payload ingestion.
- [x] **Hierarchical Trace Tree**: Reconstructs parent-child relationships and calculates duration metrics.
- [x] **Trace Query APIs**: REST APIs to list traces and fetch waterfall visualization trees.

## Phase 2: Observability UI & SQL Workbench Integration (Planned)
- [x] **Interactive Waterfall UI**: Interactive Gantt-chart style trace waterfall interface. [June 29, 2026]
- [ ] **Dependency Graph Generator**: Visual dependency graph indicating edge metrics (latency, error count).
- [x] **Database Slow Query Alerts**: Automatic highlighting of queries exceeding threshold. [June 29, 2026]

## Phase 3: High Scale & Retention (Planned)
- [ ] **ServStore Cold Tier**: Export cold trace files to S3-compatible ServStore storage.
- [ ] **Sampling Policies**: Head-based and tail-based sampling rules to filter noise.
- [ ] **Span metrics generation**: Auto-calculate throughput and latency percentiles (p50/p90/p99) on ingest.


## Phase 3: Production Observability (Next Level)
- [ ] **Trace Sampling Strategies**: Head-based and tail-based sampling with configurable rates per service.
- [ ] **Span Anomaly Detection**: Detect latency spikes and error bursts automatically. Alert via ServConsole.
- [ ] **Trace Comparison**: Compare two traces side-by-side to identify regression causes.
- [ ] **Service Map Generation**: Auto-build dependency graph from trace parent-child relationships.
- [ ] **Retention Policies**: Configurable TTL per service. Auto-archive old traces to ServStore.
- [ ] **Metrics Derivation**: Extract RED metrics (Rate, Error, Duration) from traces. No separate metrics pipeline needed.
- [ ] **Trace-to-Log Correlation**: Link trace spans to structured log entries via shared trace_id.
- [ ] **Distributed Context Baggage**: Propagate custom key-value pairs across service boundaries via trace context.
