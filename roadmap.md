# ServStore Implementation Roadmap

This document outlines the phase-wise development roadmap to transition ServStore from the MVP to a fully distributed, enterprise-grade, S3-compatible object storage platform.

---

## Phase 1: MVP Core (Completed)
Establish the single-node storage engine, S3 compatibility layer, and visual console.
- [x] Single-node deployment & storage engine
- [x] S3-compatible APIs (Bucket create/delete, Object PUT/GET/DELETE/HEAD)
- [x] Object versioning (Enabled, Suspended, Disabled) and Delete Markers
- [x] AWS Signature V4 request authentication support
- [x] Web UI Console (Glassmorphic dashboard, drag-and-drop file upload, version history inspector)
- [x] Embedded UI assets directly into single server binary

---

## Phase 2: Security, Extended Features, Observability, and CLI
Enhance the single-node capabilities with enterprise security, management utilities, and observability.
- **Extended S3 Features**:
  - [x] Multipart Upload support (Initiate, Upload Part, Complete, Abort)
  - [x] Lifecycle policies (Auto-expire/transition objects)
  - [x] Object locking (WORM - Write Once Read Many support)
- **Security & IAM**:
  - [x] TLS 1.3 enforcement
  - [x] AES-256 Encryption-at-Rest
  - [x] JWT / OIDC / LDAP Integration for console and API auth
  - [x] RBAC (Role-Based Access Control) authorization policies
- **Observability**:
  - [x] Structured logging (JSON format)
  - [x] Prometheus metrics endpoint (Request rate, latency, storage utilization, active connections)
  - [x] OpenTelemetry (OTel) tracing integration for API handlers
- **CLI Client**:
  - [x] A lightweight Go CLI (`servstore-cli`) to manage buckets, objects, policy configurations, and cluster state from the terminal

---

## Phase 3: Distributed Clustering & Data Protection
Graduate from single-node to a consistent, fault-tolerant distributed system.
- **Distributed System Foundation**:
  - [x] Cluster Membership (Gossip protocol or static configuration discovery)
  - [x] Raft Consensus Engine (For consistent metadata storage across nodes)
  - [x] Data Placement: Consistent Hashing or CRUSH-style placement algorithm
  - [x] Auto-healing (Background detection of offline drives/nodes and automatic rebuilds)
- **Data Protection & Storage Reliability**:
  - [x] Peer-to-peer data replication
  - [x] Erasure Coding (e.g. Reed-Solomon) to minimize storage overhead while preserving fault tolerance
  - [x] End-to-end data integrity validation using BLAKE3 checksums

---

## Phase 4: Horizontal Scaling & Cloud-Native Kubernetes Integration
Bring ServStore to high-scale production and Kubernetes environments.
- **Scalability**:
  - [x] Horizontal scalability (Adding nodes dynamically to increase storage capacity and throughput)
  - [x] Multi-region replication and active-active clustering
- **Kubernetes Operator**:
  - [ ] ServStore Kubernetes Operator for orchestration
  - [ ] Custom Resource Definitions (CRDs) for clusters, buckets, and access credentials
  - [ ] Helm charts for easy packaging and deployment
  - [ ] Orchestration of zero-downtime rolling upgrades of clusters
  - [ ] CSI (Container Storage Interface) Plugin support to expose ServStore buckets as persistent storage volumes
  - [ ] Dynamic Traffic Flow Control & Rate Limiting per namespace/tenant

---

## Phase 5: AI-Native Storage Engine Features
Pioneer a new class of intelligent object storage by fusing S3 with vector indexing, time travel query semantics, and serverless sandboxed computing.
- **AI & Intelligent Querying**:
  - [x] Content Addressing: Enable storage/retrieval via content hashing (`store.put(content)`) to support deduplication and Git-like addressing
  - [x] Time Travel: Query historical versions of objects at specific points in time (`bucket.at("timestamp")`) using existing version metadata
  - [x] Semantic Search: Built-in local TF-IDF embedding engine and cosine similarity ranking; S3-compatible vector search interface (`GET /bucket?query=semantic&q=<text>&max-results=N`)
  - [x] Auto-Embedding Pipeline: Automatically index text documents (`.txt`, `.md`, `text/*`) and generate vector representations upon upload; encrypted-content aware (decrypts before indexing)
- **Compute Near Data**:
  - [x] Serverless WASM Transforms: Run sandboxed WASM binaries (via `wazero` — zero-CGO pure-Go runtime) server-side directly on object streams; `POST /<bucket>/<wasm>?transform=true&target-key=<obj>&mem-limit=64&timeout=30`
  - [x] WASM Runtime Sandbox Limits: Configurable memory page limit and wall-clock timeout enforced per invocation; fresh isolated runtime per call
- **Hybrid Cloud Archiving**:
  - [x] Cold Storage Tiering: Async archival of cold CAS blocks to any S3-compatible endpoint (AWS S3 Glacier, MinIO, Backblaze B2) via stdlib `net/http`; transparent re-hydration on next `GetObject`; `.cold` stub metadata preserves remote URL; configurable min-age and sweep interval

---

## Phase 6: Enterprise Hardening & Chaos Engineering
Ensure production readiness through rigorous validation, resiliency checks, and performance benchmarks.
- **Resiliency & Validation**:
  - [ ] Jepsen Testing: Rigorous testing of the Raft FSM and cluster consensus layer under simulated network partitions
  - [ ] Chaos Mesh Integration: Simulate arbitrary disk latency, packet loss, and node crashes in Kubernetes to validate auto-healing
  - [ ] API Fuzzing: Auto-generate malformed S3 requests to ensure HTTP routing and parser stability
- **High-Performance Optimization**:
  - [ ] Direct I/O and Zero-Copy: Optimize storage engine pipelines to bypass OS page cache where appropriate for maximum disk throughput
  - [ ] Multi-threaded Hashing: Parallelize BLAKE3 checksum hashing for multi-gigabyte payload streams

