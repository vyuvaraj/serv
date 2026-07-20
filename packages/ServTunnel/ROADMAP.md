# ServTunnel Roadmap

## Differentiating Factors (Why ServTunnel vs. Ngrok/Cloudflared?)
* **Ecosystem Tracing**: Deep, zero-config integration with ServShared. Spans from incoming tunneled requests automatically connect to parent request traces and flow directly into ServConsole.
* **REST-Accessible Inspector**: Unlike proprietary dashboards, the built-in request inspector has a fully scriptable REST API for CI/CD test automation.
* **Zero Vendor Lock-in**: Self-host the relay server in one command (`servtunnel server`) with no licensing overhead.

## Current Status: v0.1.0 — MVP

### ✅ Completed (v0.1.0)
- [x] WebSocket-based tunnel protocol with JSON-framed messages
- [x] Relay server with subdomain-based routing (Host header extraction)
- [x] Tunnel client with local HTTP proxy
- [x] Request inspection ring buffer + REST API
- [x] OTel trace propagation via ServShared
- [x] Standard health endpoints (`/healthz`, `/readyz`)
- [x] Colorful terminal output for proxied requests
- [x] WebSocket keepalive (ping/pong)
- [x] Graceful shutdown
- [x] Comprehensive integration test suite
- [x] Dockerfile (multi-stage build)
- [x] GitHub Actions CI pipeline

---

### 🔲 Phase 1: Production Hardening
- [x] TLS termination (auto-provisioned Let's Encrypt certs)
- [x] Wildcard DNS setup guide for `*.servverse.net`
- [x] Authentication (API key or token for client registration)
- [x] Rate limiting (per-tunnel request rate)
- [x] Connection timeout tuning and idle disconnect
- [x] Binary body support (large file uploads)
- [x] WebSocket reconnection with exponential backoff on client

---

### 🔲 Phase 2: Developer Experience
- [x] Custom domain mapping (`dev.myapp.com` → tunnel)
- [x] Request replay endpoint (`POST /api/inspect/{id}/replay`)
- [x] Request filtering in inspector (by method, status, path)
- [x] Local web UI for request inspection (served by client)
- [x] `serv tunnel` integration in the Serv compiler CLI
- [x] Automatic subdomain based on git branch name

---

### 🔲 Phase 3: Advanced Features
- [x] Multiple simultaneous tunnels per client
- [ ] TCP tunneling (not just HTTP)
- [x] gRPC tunneling support
- [x] Tunnel sharing (team access to a tunnel)
- [x] Bandwidth monitoring and quotas
- [x] ServConsole integration (view tunnels in dashboard)
- [x] Webhook signature verification helpers

---

### 🔲 Phase 4: Scale
- [x] Multi-relay federation (distribute tunnels across regions) [July 9, 2026]
- [x] Persistent tunnel names (reserved subdomains)
- [x] Usage analytics and billing integration [July 9, 2026]
- [x] Enterprise features (SSO, audit logging, IP allowlists) [July 9, 2026]


## Phase 4: Enterprise Tunneling (Next Level)
- [x] **Team Collaboration**: Share tunnel access with team members via token-based invite links. [July 9, 2026]
- [x] **Persistent Tunnels**: Keep tunnels alive across client restarts with session resumption. [July 9, 2026]
- [x] **Custom Domain Mapping**: Map production domains to local tunnels for realistic testing. [July 9, 2026]
- [x] **Request Recording & Replay**: Record all requests through tunnel, replay them later for debugging. [July 9, 2026]
- [x] **Bandwidth Throttling**: Simulate slow networks (3G, satellite) for mobile testing. [July 9, 2026]
- [ ] **Tunnel Metrics in ServConsole**: Live throughput, latency, and connection count dashboard.
- [ ] **TCP Tunnel Support**: Tunnel raw TCP (databases, Redis, gRPC) — not just HTTP.
- [ ] **Webhook Relay Mode**: Receive webhooks on public URL, replay to multiple local services.

## Phase 5: Architectural Depth & Developer Experience (Pending)
- [ ] **`serv tunnel inspect` CLI** — Real-time view of active tunnel connections, throughput, and recent request log from the terminal (DevOps)
- [x] **Request Diff Mode** — Show a coloured diff between the proxied request and original, highlighting header mutations, body modifications or injected WASM transforms (DX) [July 9, 2026]
- [x] **Tunnel Config-as-Code** — Declare tunnel rules in `.serv/tunnel.yaml` (name, auth, subdomain, filters); committed to git for reproducible team setups (DevOps / DX) [July 9, 2026]

> See [UNIFIED_ROADMAP.md](../UNIFIED_ROADMAP.md) for the full ecosystem priority matrix and architectural recommendations.
