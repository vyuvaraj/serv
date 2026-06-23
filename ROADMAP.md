# ServCache Roadmap

This roadmap outlines the planned development phases for the ServCache distributed caching service.

---

## Differentiating Factors (Why ServCache vs. Redis/Memcached?)
* **Language-Native Abstractions**: Declare cache policies directly in `.srv` syntax (e.g. `cached fn` or `cache.set`) without writing Redis client boilerplate.
* **Service Namespacing**: Automatic namespacing keeps keys isolated between services even if sharing a single backend cache instance.
* **Integrated OTel Telemetry**: Zero-config tracing exports hit/miss/latency metrics automatically to `ServTrace` and `ServConsole`.
* **Dynamic Swap-ability**: Change cache engines from in-memory to Redis, Dragonfly, or Valkey in one line of config.

---

## Phase 1: Local In-Memory & REST API (Completed)
- [x] **In-Memory Cache Engine**: Concurrent-safe local map with key eviction loops.
- [x] **Cache REST API**: JSON endpoints for GET, SET, DELETE, and CLEAR cache states.
- [x] **TTL Eviction**: Automatically prune expired keys via background time-based cleaner.

## Phase 2: Redis Adapter & Integrations (Completed)
- [x] **Redis Engine**: Pluggable Redis/Valkey connector to leverage remote clusters.
- [x] **Dynamic Routing**: Automatic selection of in-memory or Redis backend depending on target URL schemes.
- [x] **OTel context propagation**: Span tracing context forwarding.
- [x] **GitHub Actions CI Pipeline**: Automated build and test pipeline configuration.

## Phase 3: Cluster Replications & Cache Patterns (Completed)
- [x] **Multi-Region Replication**: Cache replication across geo-distributed nodes.
- [x] **Read-Through/Write-Behind Cache**: Automatic synchronization wrappers between ServCache and ServDB.
- [x] **Key Pattern Invalidations**: Flush caches dynamically using wildcard prefix matches.
