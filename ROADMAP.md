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
- [ ] TLS termination (auto-provisioned Let's Encrypt certs)
- [ ] Wildcard DNS setup guide for `*.servverse.net`
- [x] Authentication (API key or token for client registration)
- [x] Rate limiting (per-tunnel request rate)
- [x] Connection timeout tuning and idle disconnect
- [ ] Binary body support (large file uploads)
- [x] WebSocket reconnection with exponential backoff on client

---

### 🔲 Phase 2: Developer Experience
- [ ] Custom domain mapping (`dev.myapp.com` → tunnel)
- [x] Request replay endpoint (`POST /api/inspect/{id}/replay`)
- [x] Request filtering in inspector (by method, status, path)
- [ ] Local web UI for request inspection (served by client)
- [ ] `serv tunnel` integration in the Serv compiler CLI
- [ ] Automatic subdomain based on git branch name

---

### 🔲 Phase 3: Advanced Features
- [ ] Multiple simultaneous tunnels per client
- [ ] TCP tunneling (not just HTTP)
- [ ] gRPC tunneling support
- [ ] Tunnel sharing (team access to a tunnel)
- [ ] Bandwidth monitoring and quotas
- [ ] ServConsole integration (view tunnels in dashboard)
- [ ] Webhook signature verification helpers

---

### 🔲 Phase 4: Scale
- [ ] Multi-relay federation (distribute tunnels across regions)
- [ ] Persistent tunnel names (reserved subdomains)
- [ ] Usage analytics and billing integration
- [ ] Enterprise features (SSO, audit logging, IP allowlists)
