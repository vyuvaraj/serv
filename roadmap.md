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
  - [x] Pre-signed URLs (S3-compatible temporary access generation)
- **Security & IAM**:
  - [x] TLS 1.3 enforcement
  - [x] AES-256 Encryption-at-Rest
  - [x] JWT / OIDC / LDAP Integration for console and API auth
  - [x] RBAC (Role-Based Access Control) authorization policies
- **Observability**:
  - [x] Structured logging (JSON format)
  - [x] Prometheus metrics endpoint (Request rate, latency, storage utilization, active connections)
  - [x] OpenTelemetry (OTel) tracing integration for API handlers
- **CLI Client & DevOps**:
  - [x] A lightweight Go CLI (`servstore-cli`) to manage buckets, objects, policy configurations, and cluster state from the terminal
  - [x] Automated GitHub Actions CI pipeline running builds, tests, and formatting checks
  - [x] 3-Node local clustering Docker Compose setup for instant orchestration testing

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
  - [x] ServStore Kubernetes Operator for orchestration
  - [x] Custom Resource Definitions (CRDs) for clusters, buckets, and access credentials
  - [x] Helm charts for easy packaging and deployment
  - [x] Orchestration of zero-downtime rolling upgrades of clusters
  - [x] CSI (Container Storage Interface) Plugin support to expose ServStore buckets as persistent storage volumes
  - [x] Dynamic Traffic Flow Control & Rate Limiting per namespace/tenant

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
  - [x] Jepsen Testing: Rigorous testing of the Raft FSM and cluster consensus layer under simulated network partitions
  - [x] Chaos Mesh Integration: Simulate arbitrary disk latency, packet loss, and node crashes in Kubernetes to validate auto-healing
  - [x] API Fuzzing: Auto-generate malformed S3 requests to ensure HTTP routing and parser stability
- **High-Performance Optimization**:
  - [x] Direct I/O and Zero-Copy: Optimize storage engine pipelines to bypass OS page cache where appropriate for maximum disk throughput
  - [x] Multi-threaded Hashing: Parallelize BLAKE3 checksum hashing for multi-gigabyte payload streams

---

## Phase 7: Serv-verse & Next-Gen Storage Enhancements (Proposed)
Transition ServStore into a high-capacity, metadata-optimized cluster integrated with the broader Serv ecosystem.
- **Next-Gen Storage Core**:
  - [x] **LSM-Tree Metadata Engine**: Replace basic Raft state machine file logging with a structured LSM-tree key-value store (e.g. Pebble) for sub-millisecond metadata operations at scale.
  - [x] **HNSW Vector Indexing**: Upgrade TF-IDF to a true HNSW vector index using local ONNX embeddings for advanced semantic search queries. *(Production-grade AI-native storage — priority item.)*
- **Compute Transform Enhancements**:
  - [x] **Transform Pipeline DAG Engine**: Multi-stage WASM pipeline execution via `POST /<bucket>?pipeline=true`. Stages are chained in order — stdout of each feeds stdin of the next. Pre-flight object validation, per-stage trace, optional result storage via `output_key`, and fail-fast partial trace on stage error. Powered by `pkg/pipeline`.
- **Ecosystem Integration (Serv-verse)**:
  - [x] **`/console/schema` API endpoint**: Expose table/index and bucket metadata for the ServConsole DB Inspector and Schema ORM Viewer to query.
  - [x] **Unified Management Console (ServConsole)**: Establish a single glassmorphic dashboard visualizing cluster metrics, OTel traces, rate limits, and replication state. *(ServConsole Phase 2/3 in progress.)*
  - [ ] **serv-lang Native Tooling**: Optimize client libraries and add compiler-level support for native S3 pipeline configuration.

---

## Phase 8: Operational Hardening & API Quality (Proposed — Q3 2026)

| # | Item | Effort | Description | Status |
|---|------|--------|-------------|--------|
| 8.1 | **Standardized `/healthz` and `/readyz` endpoints** | Small | Health and readiness probes for k8s liveness/readiness checks and ServConsole aggregation. | [x] |
| 8.2 | **Graceful shutdown on SIGTERM** | Small | Drain in-flight S3 requests, flush Raft log, and close cluster connections cleanly before exit. | [x] |
| 8.3 | **Standardized error response contract** | Small | Return `{"error": "msg", "code": "ERR_CODE", "trace_id": "..."}` alongside S3 XML errors for non-S3 endpoints. | [x] |
| 8.4 | **API versioning on console/admin endpoints** | Small | Version the console/admin API (`/console/v1/...`) before breaking changes accumulate. | [x] |
| 8.5 | **WebSocket push for cluster events** | Medium | Push real-time rebalance progress, node join/leave events, and replication lag updates to ServConsole. | [x] |
| 8.6 | **Bucket event notifications** | Medium | Emit events (`s3:ObjectCreated`, `s3:ObjectRemoved`) to a configurable webhook or ServQueue topic — enables event-driven architectures. | [x] |
| 8.7 | **S3 Select (query-in-place)** | Large | Support SQL-like queries on CSV/JSON objects without downloading them — high-value enterprise feature. | [x] |
| 8.8 | **Batch delete API** | Small | `POST /?delete` with XML body for bulk object deletion — missing S3 compatibility gap. | [x] |

---

## Phase 9: Next-Level Distributed Storage (Proposed — Q4 2026+)

These items take ServStore from a production-ready S3-compatible engine to a **category-defining intelligent storage platform** — competing with MinIO, Ceph, and AWS S3 while offering unique AI-native capabilities.

| # | Item | Effort | Description | Status |
|---|------|--------|-------------|--------|
| 9.1 | **Multi-modal embedding engine** | Large | Auto-generate embeddings for images (CLIP), PDFs (text extraction + embedding), and audio (Whisper transcription + embedding) on ingest — not just text. Semantic search across any content type. | [x] |
| 9.2 | **Vector similarity + metadata hybrid queries** | Medium | Combine vector search with metadata filters: `GET /bucket?query=semantic&q=cloud+architecture&filter=author:alice&after=2025-01-01`. Structured + unstructured search in one call. | [x] |
| 9.3 | **Incremental backup & point-in-time recovery** | Large | Continuous WAL-based backup to a remote target. Restore any bucket to any second in time — not just versioned objects but the exact state of the namespace. | [x] |
| 9.4 | **Object-level access logging** | Medium | Per-object access audit trail: who read/wrote/deleted, when, from which IP, with which identity. Immutable append-only log stored in a system bucket. SOC2/HIPAA prerequisite. | [x] |
| 9.5 | **S3 event notifications (CloudEvents)** | Medium | Emit CloudEvents-spec events on object lifecycle (`s3:ObjectCreated`, `s3:ObjectRemoved`, `s3:Replication`) to webhooks, ServQueue topics, or NATS subjects. | [x] |
| 9.6 | **Geo-aware data placement** | Large | Tag nodes with region/zone labels. Bucket policies like `replicate: { primary: us-east-1, secondary: eu-west-1, tertiary: ap-south-1 }`. Reads routed to nearest replica. | [x] |
| 9.7 | **Object tagging** | Small | `PUT /bucket/key?tagging` with key-value tags. Query objects by tag: `GET /bucket?tag-filter=env:prod`. S3-compatible tagging API. | [x] |
| 9.8 | **Server-side copy** | Small | `PUT /dest-bucket/key` with `x-amz-copy-source` header — copy objects between buckets without downloading. Required S3 compatibility gap. | [x] |
| 9.9 | **Bucket metrics & quota** | Medium | Per-bucket storage quota enforcement. Dashboard metrics: total size, object count, request rate, bandwidth, and growth trend. Alerts when approaching quota. | [x] |
| 9.10 | **WASM trigger on object events** | Large | Declare WASM functions that execute automatically on `PutObject` or `DeleteObject` — like AWS Lambda@S3 triggers but inside the storage engine. Zero-latency event processing. | [x] |
| 9.11 | **S3 batch operations** | Large | `POST /batch` API for bulk copy, delete, tagging, and metadata updates across thousands of objects. Job-based with progress tracking. Enterprise-scale operations. | [x] |
| 9.12 | **Content-type aware compression** | Medium | Automatically compress text, JSON, and log objects with zstd on write; decompress transparently on read. Storage reduction with zero client changes. | [x] |
| 9.13 | **Multi-user web console** | Medium | Support multiple console users with independent sessions, per-user bucket visibility, and activity history. Currently single-user embedded UI. | [x] |
| 9.14 | **Federation (cross-cluster namespace)** | Large | Federate multiple ServStore clusters under a single namespace. Global bucket names resolve to the owning cluster transparently — like DNS for objects. | [x] |

---

## Phase 10: Differentiating Factors — What No Other Storage Engine Offers (Strategic)

These create a **moat** around ServStore — capabilities that MinIO, Ceph, AWS S3, and TurboBuffer cannot replicate without fundamental architecture changes.

| # | Item | Effort | Description | Why Nobody Else Can Do This |
|---|------|--------|-------------|----------------------------|
| 10.1 | **Compute-near-data (WASM transforms on objects)** | Already Done | Execute sandboxed WASM functions server-side on stored objects — image resize, format conversion, data validation — without downloading. Lambda@S3 but inside the storage engine with zero cold start. | Pure-Go WASM runtime embedded in the storage process. AWS Lambda@S3 has 100ms+ cold start and requires separate infrastructure. MinIO doesn't execute user code. |
| 10.2 | **Semantic search on stored objects** | Already Done | Query objects by meaning, not just by key/prefix. `GET /bucket?query=semantic&q=distributed+consensus`. Auto-indexes text on upload. AWS S3 Vectors launched this in 2025 — ServStore already has it. | Built-in embedding + similarity search. AWS S3 Vectors is a separate feature, MinIO has no semantic search at all. ServStore was ahead of AWS here. |
| 10.3 | **Time travel queries** | Already Done | Retrieve any object's state at any historical timestamp — not just "version X" but "state at 2pm on Tuesday". Versioning is the mechanism; time travel is the interface. | First-class temporal API. S3 versioning requires listing all versions and filtering manually. ServStore resolves timestamps to versions automatically. |
| 10.4 | **Transform pipeline DAG** | Already Done | Chain multiple WASM transforms: `upload.pdf → extract_text.wasm → embed.wasm → index`. Multi-stage serverless pipelines inside the storage engine. | No other storage engine supports chained compute pipelines. AWS needs Step Functions + Lambda + S3 event notifications — 3 separate services. |
| 10.5 | **Content-addressed storage with reference-counted GC** | Already Done | CAS deduplication across objects. Store once, reference many. Automatic garbage collection when last reference is deleted. Git-like addressing for data. | Few storage engines do CAS with GC. MinIO does dedup via XL erasure but not content-addressable with references. |
| 10.6 | **AI-native object ingest pipeline** | Already Done | Text objects are automatically embedded and indexed on `PutObject` — no separate ETL pipeline, no external vector DB. Upload → searchable in one API call. | Auto-embedding on ingest. Competing systems require a separate pipeline (upload to S3 → trigger Lambda → call embedding API → write to Pinecone). ServStore does it in-process. |
| 10.7 | **Ecosystem-integrated event streaming** | Medium | Object lifecycle events (`PutObject`, `DeleteObject`) publish to ServQueue topics automatically. The storage engine IS the event source. No webhook relay, no S3 event notification config. | Native integration with ServQueue. AWS needs S3→SNS→SQS or S3→EventBridge. MinIO needs webhook config. ServStore publishes natively. |
| 10.8 | **Single-binary with embedded web console** | Already Done | Storage server + web UI + CLI all in one binary. No Docker, no separate admin service, no nginx. Copy one file, run it, open a browser. | Pure Go embedding. MinIO separates console into a separate service. Ceph requires a dashboard package. |
| 10.9 | **Hybrid vector+metadata queries** | Medium | `GET /bucket?query=semantic&q=architecture&filter=author:alice&after=2025-01-01` — combine semantic similarity with structured metadata filters in one query. AWS S3 Vectors + metadata filtering inspired, but unified. | Vector search + metadata filter in one engine. AWS S3 Vectors added this in GA (Dec 2025) — ServStore can match it natively. |
| 10.10 | **Cold-tier with transparent rehydration** | Already Done | Archive cold CAS blocks to any S3-compatible backend (Glacier, Backblaze B2). `GetObject` transparently re-hydrates — the client doesn't know the object was archived. No lifecycle transition delays. | Transparent lazy-load from cold storage. AWS Glacier needs explicit restore with hours of delay. ServStore rehydrates on first access. |
| 10.11 | **Language-native client (compiler-generated)** | Already Done | Serv-lang's `store "servstore://host/bucket"` compiles to a type-safe S3 client with auto-auth, connection pooling, and pipeline integration. No SDK import needed. | Compiler generates the client code. Every other storage system requires manually importing an SDK. |
| 10.12 | **AI-native positioning: Search + Compute + Store unified** | Vision | Position ServStore as the first storage engine that unifies: (1) store objects, (2) search them semantically, (3) transform them with serverless compute — all in one binary, one API, one query language. | The convergence of S3 + vector DB + serverless functions in a single engine. Nobody else offers all three together. |

> See [UNIFIED_ROADMAP.md](../UNIFIED_ROADMAP.md) for the full ecosystem priority matrix and architectural recommendations.


