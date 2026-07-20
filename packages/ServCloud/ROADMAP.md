# ServCloud Roadmap

This roadmap outlines the planned development phases for the ServCloud managed deployment platform.

---

## Differentiating Factors (Why ServCloud vs. K8s/Heroku/Nomad?)
* **Zero-Config Infrastructure**: No Dockerfiles or K8s manifests required. ServCloud compiles `.srv` files directly and infers infrastructure needs from the code.
* **Auto-Routing Gateway Sync**: Deployed services register their routes instantly with `ServGate` reverse-proxies, updating route mapping dynamically.
* **Built-in Telemetry**: Redirects standard output/error pipes to a memory ring buffer, enabling dashboard sync without external logging agents.

---

## Phase 1: Local Orchestrator MVP (Completed)
- [x] **Process Manager**: Spawns, monitors, and stops service processes dynamically.
- [x] **Go-compiler fallback**: Fallback mock server generation if native compiler is not available on path.
- [x] **Port Allocation**: Dynamically discovers and allocates free TCP ports to running services.

## Phase 2: API & Gateway Integration (Completed)
- [x] **REST API**: JSON endpoints for deployments, listing status, logs retrieval, and service deletion.
- [x] **Route Registration Sync**: Auto-updates API Gateways (like ServGate) on new deployments.
- [x] **Console log capture**: Standard output and error redirecting to in-memory ring buffer.

## Phase 3: Telemetry & Console Integration (Completed)
- [x] **ServConsole Dashboard**: Expose deployment history, rollbacks, and active process graphs in the console.
- [x] **Health Monitoring**: Periodically ping running services and flag unhealthy processes.
- [x] **CPU/Memory stats**: Query system OS metrics for resource consumption monitoring.

## Phase 4: Production Isolation & Security (Planned)
- [x] **WASM Isolation**: Direct execution of compiled WASM targets in-process for sandbox isolation. [June 29, 2026]
- [x] **Docker Engine runner**: Spin up individual services in isolated Docker containers instead of native processes. [June 29, 2026]
- [x] **Shared OIDC Authentication**: Enforce bearer token validation via shared `SERV_JWT_SECRET`. [June 29, 2026]


## Phase 5: Production PaaS Features (Next Level — Pending)
- [ ] **Resource Quotas & Limits**: Per-deployment CPU/memory caps with OOM protection.
- [ ] **Secret Injection from ServStore**: Resolve `${{secrets.KEY}}` references from encrypted ServStore bucket at deploy time. Rotate secrets without redeployment.
- [ ] **ServAuth Integration**: Auto-provision ServAuth OIDC configuration for deployed services. Services get identity management out of the box.
- [ ] **Build Packs**: Auto-detect project type (Go, Node, Python, Serv) and build without user-provided Dockerfile.
- [ ] **Deployment Previews**: Branch-based preview deployments with unique URLs (like Vercel previews).
- [ ] **Horizontal Auto-scaling**: Scale instances up/down based on request rate from ServGate metrics.
- [ ] **Integrated CI Pipeline**: Run `serv test` before deploy. Reject deploys that fail tests.
- [ ] **Multi-region Deployment**: Deploy to multiple regions with ServMesh-based global load balancing.

## Phase 6: Architectural Depth & DevOps (Pending)
- [ ] **GitOps Deployment Sync** — Trigger deploys automatically on git push via webhook; store deployment manifest in repository for auditability (OPS.5)
- [ ] **`serv cloud diff`** — Preview infrastructure changes (environment vars, resources, routes) before applying a deploy — like `terraform plan` for ServCloud (DevOps)
- [ ] **Deploy Annotations** — Annotate each deploy with commit SHA, author, and changelog; surface in ServConsole timeline and in ServTrace spans for change correlation (DX)
- [ ] **Local `serv cloud emulate`** — Emulate the full production deploy pipeline locally: health checks, rolling update, rollback — catching breakage before pushing (DX)

> See [UNIFIED_ROADMAP.md](../servverse-repo/UNIFIED_ROADMAP.md) for the full ecosystem priority matrix.


---

## Phase 7: Test Coverage (Pending — Phase 22)

> **Issue:** Only 7 test functions in 1 file.

| # | Item | Effort | Description | Status |
|---|------|--------|-------------|--------|
| 7.1 | **Expand test suite** | Medium | From 7 → 30+ test functions: deploy lifecycle, port allocation conflicts, health monitoring recovery, gateway route sync, Docker runner isolation, rollback | [ ] |
