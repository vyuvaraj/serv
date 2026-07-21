# Servverse (`serv`) Monorepo

> **The Unified & Modular Open-Source Backend Infrastructure Engine**

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL_v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8.svg?style=flat&logo=go)](https://golang.org)
[![Monorepo Status](https://img.shields.io/badge/Packages-19_Modules-purple.svg)](#packages--component-catalog)
[![Documentation](https://img.shields.io/badge/Docs-servverse.github.io-green.svg)](https://vyuvaraj.github.io/servverse/)

**Servverse** is a complete, modular backend infrastructure engine designed to build high-performance microservices with zero glue code. It provides a programming language (`Serv-lang`), API gateway (`ServGate`), message broker (`ServQueue`), S3 object store (`ServStore`), cache engine (`ServCache`), identity provider (`ServAuth`), and distributed tracing dashboard (`ServConsole`).

Deploy as a **unified platform** using `Serv-lang`, or run any component **independently as a standalone Go/WASM binary** inside existing Node.js, Python, Java, Go, or Rust microservice stacks.

---

## 🧩 Unified Platform vs. Standalone Go/WASM Tools

| Mode | Deployment | Best For |
|---|---|---|
| **Unified Platform** | Run all 19 packages in concert via `serv dev main.srv` or `serv deploy` | Greenfield backend services, all-in-one API stacks, and rapid prototyping without glue code. |
| **Standalone Component** | Build & deploy any individual package (e.g. `ServGate`, `ServStore`, `ServQueue`) as a zero-dependency Go binary | Existing Node.js, Python, Java, or Go stacks needing an AI API Gateway, S3 vector search, or inline WASM stream processing. |

---

## 📦 Packages & Component Catalog

All packages live under [`/packages`](./packages) and are integrated into a single Go Workspace (`go.work`):

| Component | Path | Description | Key Features |
|---|---|---|---|
| **Serv-lang** | [`packages/Serv-lang`](./packages/Serv-lang) | Compiler & Language Runtime | Domain-specific backend language compiling to native Go binaries. |
| **ServGate** | [`packages/ServGate`](./packages/ServGate) | Standalone API Gateway & AI Guard | WASM reverse proxy, AI Prompt Guard, PII redaction, Circuit Breaker, Sliding-window rate limiting. |
| **ServQueue** | [`packages/ServQueue`](./packages/ServQueue) | Standalone STOMP Message Broker | Compute-in-Queue WASM stream transforms (50ms sandboxing), DLQ auto-offloading, memory backpressure. |
| **ServStore** | [`packages/ServStore`](./packages/ServStore) | Standalone S3 Storage + Vector Search | S3-compatible object storage with embedded TF-IDF/vector search and time-travel versioning. |
| **ServCache** | [`packages/ServCache`](./packages/ServCache) | Distributed Cache Engine | Dual-mode memory/Redis caching with TTL, sliding eviction, and OTel metrics. |
| **ServAuth** | [`packages/ServAuth`](./packages/ServAuth) | Identity & Access Provider | OAuth2/OIDC provider, multi-tenant RBAC, MFA, JWT validation middleware. |
| **ServConsole** | [`packages/ServConsole`](./packages/ServConsole) | Observability Dashboard | Central web dashboard, metrics visualizer, SQL workbench, and incident analyzer. |
| **ServMesh** | [`packages/ServMesh`](./packages/ServMesh) | Library Service Mesh | Client-side service discovery, load balancing, retries, and circuit breaking. |
| **ServCron** | [`packages/ServCron`](./packages/ServCron) | Distributed Job Scheduler | Multi-node leader election, cron schedule parser, persistent state via ServStore. |
| **ServCloud** | [`packages/ServCloud`](./packages/ServCloud) | Deployment Orchestrator | Single-command Docker, Kubernetes, and TLS certificate deployment pipeline. |
| **ServTrace** | [`packages/ServTrace`](./packages/ServTrace) | Distributed Tracing Engine | OTLP trace collector, waterfall UI, and trace anomaly detection. |
| **ServTunnel** | [`packages/ServTunnel`](./packages/ServTunnel) | WebSocket Dev Tunneling | WebSocket relay server, custom subdomain routing, and HTTP traffic inspector. |
| **ServPool** | [`packages/ServPool`](./packages/ServPool) | Database Connection Proxy | Connection pooling, SQL query analytics, and read/write connection splitting. |
| **ServMail** | [`packages/ServMail`](./packages/ServMail) | Notification Gateway | Transactional email, Slack, SMS delivery gateway with HTML templating. |
| **ServFlow** | [`packages/ServFlow`](./packages/ServFlow) | Workflow Engine | Stateful DAG execution engine, saga orchestrator, and human approval gates. |
| **ServRegistry** | [`packages/ServRegistry`](./packages/ServRegistry) | Package Management | Semver resolution, artifact signing, and module registry service. |
| **ServShared** | [`packages/ServShared`](./packages/ServShared) | Ecosystem Shared Core | Resilient exponential backoff retry utility (`pkg/retry`), health checks, and OTel middleware. |
| **servlockctl** | [`packages/servlockctl`](./packages/servlockctl) | Distributed Lock CLI | Command-line tool & daemon for acquiring, renewing, and inspecting distributed locks. |
| **servsecretctl**| [`packages/servsecretctl`](./packages/servsecretctl)| Secret Management CLI | CLI utility for dynamic secret injection into sub-processes and Shamir key unsealing. |

---

## ⚡ Quickstart

### Prerequisites
* **Go 1.22+** installed on system.

### Build All Modules
```bash
# Clone the repository
git clone https://github.com/vyuvaraj/serv.git
cd serv

# Test all packages in workspace
go test github.com/vyuvaraj/serv/packages/...

# Build compiler binary
cd packages/Serv-lang
go build -o serv main.go
```

---

## 🏢 Enterprise Edition (EE)

For enterprise compliance, multi-tenant RBAC, audited Raft clustering, and dedicated OIDC/SAML integration, see the **Servverse Enterprise Edition (EE)** repository:
* **Enterprise Repo:** [`github.com/vyuvaraj/serv-ee`](https://github.com/vyuvaraj/serv-ee) *(Private / License Required)*

---

## 📄 License

This monorepo is open-source software licensed under the **GNU Affero General Public License v3.0 (AGPL-3.0)**. See the [LICENSE](./LICENSE) file for full details.
