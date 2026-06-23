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
- [ ] **Topology Map**: Real-time dependency graph visualization in ServConsole.
- [ ] **Breaker Alerting**: Sends telemetry signals to alert on circuit-breaker trips.
- [ ] **Dynamic Routing Rules**: Update client-side routing and retries dynamically via registry config.
