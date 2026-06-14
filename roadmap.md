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
  - [ ] End-to-end data integrity validation using BLAKE3 checksums

---

## Phase 4: Horizontal Scaling & Cloud-Native Kubernetes Integration
Bring ServStore to high-scale production and Kubernetes environments.
- **Scalability**:
  - [ ] Horizontal scalability (Adding nodes dynamically to increase storage capacity and throughput)
  - [ ] Multi-region replication and active-active clustering
- **Kubernetes Operator**:
  - [ ] ServStore Kubernetes Operator for orchestration
  - [ ] Custom Resource Definitions (CRDs) for clusters, buckets, and access credentials
  - [ ] Helm charts for easy packaging and deployment
  - [ ] Orchestration of zero-downtime rolling upgrades of clusters

---

## Phase 5: AI-Native Storage Engine Features
Pioneer a new class of intelligent object storage by fusing S3 with vector indexing, time travel query semantics, and serverless sandboxed computing.
- **AI & Intelligent Querying**:
  - [ ] Content Addressing: Enable storage/retrieval via content hashing (`store.put(content)`) to support deduplication and Git-like addressing
  - [ ] Time Travel: Query historical versions of objects at specific points in time (`bucket.at("timestamp")`) using existing version metadata
  - [ ] Semantic Search: Built-in local embedding generation and vector search (`store.search("query")`) to retrieve objects semantically
- **Compute Near Data**:
  - [ ] Serverless WASM Transforms: Run sandboxed WASM binaries server-side directly on object streams (`bucket.map(transform)`) using `wazero`
