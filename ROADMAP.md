# ServMesh Roadmap

This roadmap outlines the planned development phases for the ServMesh library-level service mesh.

---

## Differentiating Factors (Why ServMesh vs. Istio/Linkerd?)
* **Library-Level (No Sidecars)**: Runs directly within the client binary via custom transport, bypassing the CPU/Memory overhead and network latency of sidecar proxies (like Envoy).
* **Native Language Integration**: Directly resolves `serv://` schemas in code, bringing service discovery semantics directly into the application layer.
* **Out-of-the-Box Resilience**: Seamlessly bundles retries, round-robin load-balancing, OTel trace context, and circuit breakers with zero external yaml configuration.

---

## Phase 1: Service Registry & Resolver (Completed)
- [x] **Service Registry**: Centralized control plane daemon for registering active service instances.
- [x] **Registry API**: JSON endpoints for registration, heartbeat updates, and name resolution.
- [x] **Client-Side HTTP Resolver**: Custom HTTP `RoundTripper` that intercepts `serv://` targets.
- [x] **TTL heartbeats**: Automatic pruning of stale/inactive endpoints.

## Phase 2: Load Balancing & Resilience (Completed)
- [x] **Round-Robin Load Balancing**: Balances client requests evenly across all healthy service replicas.
- [x] **Circuit Breaker**: Tracks failures, managing states (`Closed`, `Open`, `Half-Open`) to prevent cascades.
- [x] **Automatic Retries**: Implements exponential backoff retries on request timeouts.
- [x] **OTel Context Propagation**: Transparent trace header injection via ServShared.

## Phase 3: Security & Network Controls (Completed)
- [x] **Dynamic mTLS**: Auto-provisioned client/server certificates for zero-trust inter-service encryption.
- [x] **Registry JWT Protection**: Secure API registration with shared `SERV_JWT_SECRET` verification.
- [x] **Canary Traffic Splitting**: Route percentage of traffic to new versions at client-side.

## Phase 4: Console & Observability (Planned)
- [x] **Topology Map**: Real-time dependency graph visualization in ServConsole. [June 29, 2026]
- [x] **Breaker Alerting**: Sends telemetry signals to alert on circuit-breaker trips. [June 29, 2026]
- [x] **Dynamic Routing Rules**: Update client-side routing and retries dynamically via registry config. [June 29, 2026]


## Phase 5: Advanced Traffic Management (Next Level)
- [x] **Rate Limiting per Service Pair**: Client-side token-bucket rate limiting keyed on caller→callee identity; returns HTTP 429 when burst is exhausted. [July 9, 2026]
- [x] **Fault Injection Testing**: Inject artificial latency, errors, or timeouts via registry config for chaos engineering. [June 30, 2026]
- [x] **Service Versioning & Header Routing**: Route requests to specific versions via `X-Service-Version` header with graceful fallback. Blue/green at service mesh level. [July 9, 2026]
- [ ] **Health-Aware Load Balancing**: Weight routing based on real-time latency/error-rate feedback from OTel spans.
- [x] **Sidecar-less mTLS Rotation**: Automated cert rotation with zero-downtime using background goroutine (SEC.7)
- [ ] **Service Dependency Graph API**: Expose real-time dependency topology data from heartbeat metadata for ServConsole.
- [ ] **gRPC Support**: Extend the resolver and circuit breaker to handle gRPC connections natively.

## Phase 6: Architectural Depth & DevOps (Pending)
- [x] **Ecosystem Performance Suite** — Multi-tiered Go micro-benchmarks, k6 component load tests, and distributed end-to-end telemetry workloads (OPS.7)
- [x] **Automated mTLS Rotation** — Certificate lifecycle management with automatic renewal and zero-downtime rotation (SEC.7)
- [ ] **Local Dev Service Mesh** — One-command `serv mesh up` that starts a local registry + resolver with zero config for fast developer iteration (DX.8)
- [ ] **Mesh Topology CLI** — `servmesh inspect` command showing live service-to-service call graph, circuit breaker states, and latency distribution (DevOps)

> See [UNIFIED_ROADMAP.md](../UNIFIED_ROADMAP.md) for the full ecosystem priority matrix and architectural recommendations.
