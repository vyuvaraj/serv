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


## Phase 4: Intelligent Caching & Ecosystem (Next Level)
- [ ] **Predictive Pre-warming**: Analyze access patterns via OTel traces to pre-load hot keys before they're requested. ML-based hit-rate optimization.
- [ ] **Cache-Aside Codegen in Serv-lang**: Compile `cached fn getData()` syntax directly to ServCache GET/SET calls with automatic invalidation.
- [ ] **Distributed Cache Coherence (Gossip)**: Multi-node cache with gossip-based invalidation protocol. No single point of failure.
- [ ] **Write-Coalescing**: Batch rapid writes to the same key into a single backend write (debounce pattern).
- [ ] **Cache Analytics Dashboard**: Hit/miss ratios, latency percentiles, eviction rates, and memory pressure per namespace in ServConsole.
- [ ] **Tag-based Invalidation**: Assign tags to keys (`user:123`, `product:*`), then invalidate by tag group in one call.
- [ ] **Tiered Storage**: Hot keys in memory → warm keys in Redis → cold keys in ServStore. Automatic promotion/demotion.

## Phase 5: Architectural Depth & Developer Experience (Pending)
- [ ] **Adaptive Pool Invalidation** — Automatically invalidate cache entries tied to ServDB writes using a change-data-capture hook (PS.1)
- [ ] **`serv cache inspect` CLI** — CLI command to display per-namespace key counts, memory usage, hit/miss ratios, and top hot keys for developer debugging (DevOps)
- [ ] **Cache Warming Fixtures** — Load a named fixtures file (`serv cache warm --fixture dev.yaml`) on service start for reproducible local development environments (DX)

> See [UNIFIED_ROADMAP.md](../UNIFIED_ROADMAP.md) for the full ecosystem priority matrix and architectural recommendations.
