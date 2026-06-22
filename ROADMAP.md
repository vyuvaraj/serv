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
- [ ] **Interactive Waterfall UI**: Interactive Gantt-chart style trace waterfall interface.
- [ ] **Dependency Graph Generator**: Visual dependency graph indicating edge metrics (latency, error count).
- [ ] **Database Slow Query Alerts**: Automatic highlighting of queries exceeding threshold.

## Phase 3: High Scale & Retention (Planned)
- [ ] **ServStore Cold Tier**: Export cold trace files to S3-compatible ServStore storage.
- [ ] **Sampling Policies**: Head-based and tail-based sampling rules to filter noise.
- [ ] **Span metrics generation**: Auto-calculate throughput and latency percentiles (p50/p90/p99) on ingest.
