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
- [ ] **WASM Isolation**: Direct execution of compiled WASM targets in-process for sandbox isolation.
- [ ] **Docker Engine runner**: Spin up individual services in isolated Docker containers instead of native processes.
- [ ] **Shared OIDC Authentication**: Enforce bearer token validation via shared `SERV_JWT_SECRET`.
