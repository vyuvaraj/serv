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
  - [ ] Multipart Upload support (Initiate, Upload Part, Complete, Abort)
  - [ ] Lifecycle policies (Auto-expire/transition objects)
  - [ ] Object locking (WORM - Write Once Read Many support)
- **Security & IAM**:
  - [ ] TLS 1.3 enforcement
  - [ ] AES-256 Encryption-at-Rest
  - [ ] JWT / OIDC / LDAP Integration for console and API auth
  - [ ] RBAC (Role-Based Access Control) authorization policies
- **Observability**:
  - [ ] Structured logging (JSON format)
  - [ ] Prometheus metrics endpoint (Request rate, latency, storage utilization, active connections)
  - [ ] OpenTelemetry (OTel) tracing integration for API handlers
- **CLI Client**:
  - [ ] A lightweight Go CLI (`servstore-cli`) to manage buckets, objects, policy configurations, and cluster state from the terminal

---

## Phase 3: Distributed Clustering & Data Protection
Graduate from single-node to a consistent, fault-tolerant distributed system.
- **Distributed System Foundation**:
  - [ ] Cluster Membership (Gossip protocol or static configuration discovery)
  - [ ] Raft Consensus Engine (For consistent metadata storage across nodes)
  - [ ] Data Placement: Consistent Hashing or CRUSH-style placement algorithm
  - [ ] Auto-healing (Background detection of offline drives/nodes and automatic rebuilds)
- **Data Protection & Storage Reliability**:
  - [ ] Peer-to-peer data replication
  - [ ] Erasure Coding (e.g. Reed-Solomon) to minimize storage overhead while preserving fault tolerance
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
