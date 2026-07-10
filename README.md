# ServStore

```bash
docker run -p 8081:8081 ghcr.io/vyuvaraj/servstore:latest
```

[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Build](https://img.shields.io/badge/build-passing-brightgreen)](https://github.com/vyuvaraj/ServStore)
[![S3 Compatible](https://img.shields.io/badge/S3-Compatible-FF9900?logo=amazon-s3&logoColor=white)](https://github.com/vyuvaraj/ServStore)
[![WASM](https://img.shields.io/badge/WASM-Compute--Near--Data-654FF0?logo=webassembly&logoColor=white)](https://github.com/vyuvaraj/ServStore)

> A cloud-native, distributed, AI-native, S3-compatible object storage engine.  
> ServStore is an open-source alternative to MinIO — built for strong consistency, high scalability, and intelligent data access.

---

## About

ServStore is a **production-grade distributed object storage engine** written in pure Go. It was designed to be a self-hostable, cloud-native alternative to MinIO and AWS S3 — while going further with an AI-native storage layer that brings semantic search, time travel queries, and serverless compute-near-data directly into the storage engine.

| Property | Detail |
|---|---|
| **Author** | [yuvaraj](https://github.com/vyuvaraj) |
| **Language** | Go 1.22+ |
| **License** | Apache 2.0 |
| **S3 API** | AWS S3 REST compatible (Signature V4) |
| **Consensus** | Raft-based strong consistency |
| **WASM Runtime** | wazero (pure-Go, zero-CGO) |
| **Status** | Phases 1–6 complete ✅ |

ServStore now combines a production-grade distributed storage engine with a full AI-native layer: semantic search, time travel queries, serverless compute-near-data (WASM), and hybrid cloud cold-storage tiering.

---

## Key Features

### S3 Compatibility & Core Storage
* **S3-Compatible REST API**: Full support for bucket and object lifecycle — `PUT`, `GET`, `DELETE`, `HEAD`, list, delete markers, and S3-style XML responses.
* **S3 Multipart Uploads**: `InitiateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, and `AbortMultipartUpload` for streaming large files.
* **Object Versioning**: Enabled / Suspended / Disabled states matching AWS S3 semantics. Delete markers and permanent version deletion supported.
* **Object Locking (WORM)**: Write-once-read-many locks with configurable retain-until dates. Locked objects reject modification and deletion until expiry.
* **Lifecycle Policies**: Auto-expire objects after N days. Configurable prefix-scoped rules applied by a background goroutine.

### Security & Access Control
* **AWS Signature V4 Authentication**: Full HMAC-SHA256 request signing compatible with any S3 SDK.
* **AES-256-GCM Encryption-at-Rest**: Optional per-object encryption enabled via `--encryption-key`. Passphrase is SHA-256 derived. Fully transparent to S3 clients.
* **TLS 1.3 Enforcement**: Optional HTTPS with forced TLS 1.3 minimum. Graceful HTTP fallback when not configured.
* **JWT / OIDC / LDAP Integration**: Console and API authentication via configurable identity providers.
* **RBAC**: Role-based access control with user policy management (`PutUserPolicy`, `GetUserPolicy`, `DeleteUserPolicy`).

### Distributed System
* **Gossip Membership Protocol**: Lightweight node discovery and failure detection. Nodes detect and evict unresponsive peers automatically.
* **Raft Consensus Engine**: All metadata mutations proposed through Raft for strong consistency across the cluster.
* **Consistent Hash Ring**: Virtual-node hash ring (CRUSH-style) for balanced data placement. Node weight adjustable at runtime.
* **Horizontal Scaling**: Add nodes dynamically via `POST /console/cluster/join`. Background rebalancer redistributes existing objects to new nodes without downtime.
* **Peer-to-Peer Auto-healing**: Detects offline nodes, identifies under-replicated objects, and rebuilds replicas in the background.
* **Reed-Solomon Erasure Coding**: Configurable data/parity shard ratio (default 2+1). Tolerates shard loss without full replication overhead.
* **Cross-Region Replication (CRR)**: Asynchronously replicates PUT/DELETE across geographic regions. Loop-prevention via `X-ServStore-Region-Source` headers. Active-active topology.
* **BLAKE3 Data Integrity**: End-to-end checksums computed on PUT, validated on GET. Detects bit rot and storage corruption on-the-fly with failover to healthy replicas.

### AI-Native Storage Engine (Phase 5)
* **Content-Addressed Storage (CAS)**: Enable deduplication on any bucket. Objects are stored as `cas-<blake3>`, with reference-counted GC — data is only deleted when the last reference is removed.
* **Time Travel Queries**: Retrieve the state of any object at any historical timestamp: `GET /<bucket>/<key>?at=2025-01-01T00:00:00Z`. Resolved against version `LastModified` metadata — no extra storage overhead.
* **Semantic Search**: Built-in TF-IDF vector indexing on text objects. Cosine similarity ranking. S3-compatible query API: `GET /<bucket>?query=semantic&q=<text>&max-results=N`. Encryption-aware (decrypts before indexing).
* **Auto-Embedding Pipeline**: Text documents (`.txt`, `.md`, `text/*`) are automatically indexed on `PutObject` — no explicit pipeline step required.
* **Serverless WASM Transforms (Compute Near Data)**: Upload any WASI-compatible `.wasm` binary as an object. Execute it server-side against any other object via `POST /<bucket>/<wasm>?transform=true&target-key=<obj>&mem-limit=64&timeout=30`. Powered by `wazero` — pure-Go, zero-CGO, zero-host-filesystem-access.
* **WASM Sandbox Limits**: Configurable memory page ceiling and wall-clock timeout per invocation. Each call gets a fresh, isolated `wazero.Runtime` instance — no shared mutable state.
* **Cold Storage Tiering**: Async archival of cold CAS blocks to any S3-compatible cold-storage backend (AWS S3 Glacier, Backblaze B2, MinIO). Transparent re-hydration on `GetObject`. `.cold` stub tracks remote URL, archive time, and size. Zero new dependencies — uses stdlib `net/http`.

### Observability & Operations
* **OpenTelemetry Tracing**: Lightweight custom OTel client exporting spans for all HTTP routes and storage I/O. Zero external dependencies.
* **Prometheus Metrics**: Custom registry exposing request rate, latency histograms, storage utilization, in-flight connection counts, and cluster state at `/metrics`.
* **Structured JSON Logging**: All requests logged in structured `slog` JSON format with trace IDs, method, path, status, and duration.

### Developer Experience
* **Single-Binary Deployment**: Frontend web console assets are embedded directly into the compiled Go binary. Zero file dependencies at runtime.
* **Web Console**: Premium glassmorphic dark-mode admin UI with drag-and-drop uploads, bucket management, versioning controls, and object version history viewer.
* **ServStore CLI (`servstore-cli`)**: Terminal client supporting `mb`, `rb`, `ls`, `put`, `get`, `rm`, `policy`, and cluster management commands against any ServStore endpoint.
* **serv-lang Native Client**: Out-of-the-box integration with the [serv-lang](file:///f:/Don/Serv/Serv-lang) DSL standard library (`stdlib/s3.srv`), supporting bucket creation, versioning, CRUD, time travel, and semantic search.

---

## Directory Structure
```text
ServStore/
├── cmd/
│   ├── servstore/
│   │   └── main.go                   # Server entry point, CLI flags, TLS & encryption config
│   ├── servstore-cli/
│   │   └── main.go                   # CLI client (mb, rb, ls, put, get, rm, policy)
│   ├── operator/
│   │   └── main.go                   # Kubernetes Operator Manager binary
│   └── csi-driver/
│       └── main.go                   # CSI Node Plugin gRPC stub
├── deploy/
│   ├── crds/                         # Kubernetes Custom Resource Definitions (CRDs)
│   │   ├── servstorebucket.yaml
│   │   ├── servstorecluster.yaml
│   │   └── servstorecredential.yaml
│   └── helm/
│       └── servstore/                # Kubernetes Helm Chart for Cluster & Operator
├── pkg/
│   ├── auth/
│   │   └── auth.go                   # AWS Signature V4 authentication + JWT/OIDC/LDAP
│   ├── cluster/
│   │   ├── membership.go             # Gossip protocol, node timeouts & Hash Ring logic
│   │   ├── healing.go                # P2P auto-healing & dynamic rebalancing
│   │   ├── crr.go                    # Cross-Region Replication (CRR) Manager
│   │   ├── placement.go              # Consistent hashing ring implementation
│   │   ├── raft_node.go              # Raft consensus node for consistent metadata
│   │   ├── rebalance_test.go         # Integration tests for dynamic scale-out rebalancing
│   │   └── crr_test.go               # Integration tests for Cross-Region Replication
│   ├── metrics/
│   │   ├── metrics.go                # Zero-dependency Prometheus metrics registry
│   │   └── metrics_test.go           # Unit tests for metrics serialisation
│   ├── operator/
│   │   ├── register.go               # Scheme registration for CRDs
│   │   ├── types.go                  # Go spec and status structures for CRDs
│   │   └── controllers/
│   │       ├── cluster_controller.go # StatefulSet & Rolling Upgrade reconciler
│   │       ├── bucket_controller.go  # S3 bucket configuration reconciler
│   │       ├── credential_controller.go # Secret to S3 policy mapping reconciler
│   │       └── operator_test.go      # Operator unit tests
│   ├── otel/
│   │   ├── otel.go                   # Lightweight OpenTelemetry tracing client
│   │   └── otel_test.go              # Unit tests for OTel tracing
│   ├── ratelimit/
│   │   ├── limiter.go                # Tenant-isolated token-bucket rate limiter
│   │   └── limiter_test.go           # Limiter unit tests
│   ├── s3/
│   │   ├── api.go                    # S3 API router, gateway handlers & failover routing
│   │   ├── xml.go                    # S3-compliant XML request/response models
│   │   └── integrity_failover_test.go # Integration tests for BLAKE3 data integrity failover
│   ├── storage/
│   │   ├── store.go                  # StorageEngine interface definition
│   │   ├── local_store.go            # Versioned storage, CAS, encryption, WASM, cold tier
│   │   ├── crypto.go                 # AES-256-GCM encrypt/decrypt helpers
│   │   ├── vector.go                 # TF-IDF tokeniser & cosine similarity engine
│   │   ├── cold_tier.go              # Cold storage tiering — archive, stub, re-hydration
│   │   ├── cas_test.go               # Integration tests for CAS deduplication
│   │   ├── semantic_test.go          # Integration tests for semantic search
│   │   ├── time_travel_test.go       # Integration tests for time travel queries
│   │   ├── cold_tier_test.go         # Integration tests for cold storage tiering
│   │   ├── integrity_test.go         # Unit tests for BLAKE3 checksums & bit rot detection
│   │   ├── crypto_test.go            # Unit tests for encryption round-trips
│   │   └── local_store_test.go       # Storage engine test suite (versioning, multipart, WORM)
│   ├── wasm/
│   │   ├── runner.go                 # Sandboxed wazero WASI execution engine
│   │   └── runner_test.go            # Tests for WASM execution (uppercase, passthrough, limits)
│   └── web/
│       ├── server.go                 # Web Console static asset and API router wrapper
│       └── assets/                   # Web Console files (index.html, style.css, app.js)
├── go.mod                            # Module definition (github.com/tetratelabs/wazero added)
├── roadmap.md                        # Phase-wise implementation roadmap
└── README.md                         # Product documentation
```

---

## Getting Started

### Prerequisites
* Go 1.22 or higher

### 1. Run Tests
```bash
go test ./...
```

### 2. Build the Server
```bash
go build -o servstore ./cmd/servstore
```

### 3. Run the Server

**Basic (no auth, port 9000):**
```bash
./servstore
```

**With AWS Signature V4 auth:**
```bash
./servstore --auth --access-key "yourAccessKey" --secret-key "yourSecretKey"
```

**With AES-256 encryption at rest:**
```bash
./servstore --encryption-key "my-strong-passphrase"
```

**With TLS 1.3:**
```bash
openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -days 365 -nodes -subj "/CN=localhost"
./servstore --tls-cert ./server.crt --tls-key ./server.key
```

**With OpenTelemetry tracing:**
```bash
$env:OTEL_ENDPOINT="http://localhost:4318"
$env:OTEL_SERVICE_NAME="servstore"
./servstore
```

### 4. Open the Web Console
Navigate to [http://localhost:9000](http://localhost:9000). From here you can:
* Create and delete buckets.
* Toggle versioning (Enabled / Suspended).
* Drag and drop files to upload them.
* Inspect metadata, download past versions, or permanently delete them.

---

## AI-Native API Examples

### Semantic Search
```bash
# Upload text documents (auto-indexed on ingest)
aws s3api put-object --bucket docs --key raft.txt --body raft.txt --content-type text/plain --endpoint-url http://localhost:9000

# Semantic search — returns ranked XML like ListObjects
curl "http://localhost:9000/docs?query=semantic&q=consensus+metadata+replication&max-results=3"
```

### Time Travel
```bash
# Retrieve object state at a specific point in time
curl "http://localhost:9000/mybucket/config.json?at=2025-06-01T12:00:00Z"

# Via aws CLI
aws s3api get-object --bucket mybucket --key config.json \
  --query-string '?at=2025-06-01T12:00:00Z' /tmp/config-snapshot.json \
  --endpoint-url http://localhost:9000
```

### WASM Compute-Near-Data
```bash
# Build and upload a WASI transform binary
GOOS=wasip1 GOARCH=wasm go build -o uppercase.wasm ./transforms/uppercase/
aws s3api put-object --bucket transforms --key uppercase.wasm --body uppercase.wasm --endpoint-url http://localhost:9000

# Upload the data to transform
aws s3api put-object --bucket transforms --key hello.txt --body hello.txt --endpoint-url http://localhost:9000

# Execute the transform server-side (returns transformed bytes)
curl -X POST "http://localhost:9000/transforms/uppercase.wasm?transform=true&target-key=hello.txt&mem-limit=64&timeout=30"
```

### Cold Storage Tiering
```bash
# Configure cold tiering for a CAS bucket
curl -X PUT "http://localhost:9000/mybucket?cold-tier" \
  -H "Content-Type: application/json" \
  -d '{
    "endpoint": "https://s3.amazonaws.com",
    "remote_bucket": "cold-archive",
    "region": "us-east-1",
    "access_key": "AKIA...",
    "secret_key": "...",
    "min_age_days": 30,
    "scan_interval_min": 60
  }'

# Trigger an immediate sweep
curl -X POST "http://localhost:9000/mybucket?cold-tier&sweep"
# {"archived":5,"errors":[]}

# GetObject transparently re-hydrates archived blocks — no API changes needed
aws s3api get-object --bucket mybucket --key archived.bin /tmp/out.bin --endpoint-url http://localhost:9000
```

---

## Kubernetes & Cloud-Native Deployment (Phase 4)

ServStore includes a custom Kubernetes Operator, Helm Chart package, and Container Storage Interface (CSI) node plugin.

### 1. Custom Resource Definitions (CRDs)
Deploy ServStore resources declaratively in Kubernetes:
```yaml
# Create a 3-node S3 storage cluster with Reed-Solomon Erasure Coding enabled
apiVersion: storage.servstore.io/v1alpha1
kind: ServStoreCluster
metadata:
  name: my-s3-cluster
spec:
  replicas: 3
  image: ghcr.io/vyuvaraj/servstore:latest
  erasureCoding:
    enabled: true
    dataShards: 2
    parityShards: 1
  storage:
    size: 50Gi
```

### 2. Deploy using Helm
```bash
# Template or deploy the cluster and operator
helm install my-release ./deploy/helm/servstore
```

### 3. Tenant Rate Limiting
Apply QoS rate limits per namespace/tenant:
```bash
# Set rate-limiting header in requests to S3 Gateway
curl -H "X-ServStore-Namespace: tenant-alpha" http://localhost:9000/mybucket/file.txt
```
If traffic limits are exceeded, ServStore responds with `HTTP 429 Too Many Requests` and a dynamic `Retry-After` header.

---

## Enterprise Hardening & Performance (Phase 6)

### 1. Resiliency & Chaos Mesh
Manifests are provided in `deploy/chaos/` to execute automated testing:
- **`pod-chaos.yaml`**: Intermittently fails pods to verify Raft leader re-election stability.
- **`network-chaos.yaml`**: Simulates packet loss and network delay to verify gossip protocols.
- **`io-chaos.yaml`**: Injects simulated read/write disk errors on `/data` to test S3 gateway failover routing.

### 2. Direct I/O Bypass
For objects larger than 16MB, ServStore automatically uses sector-aligned Direct I/O (`FILE_FLAG_NO_BUFFERING` on Windows) to bypass the OS page cache for direct disk throughput.

### 3. Parallel Hashing
When processing large objects (>8MB), ServStore automatically parallelizes BLAKE3 checksum calculations across multiple CPU threads using concurrent chunk hashing, performing root tree reduction.

---


## Roadmap

See [roadmap.md](roadmap.md) for the full phase-by-phase implementation plan. **All Phases 1–6 are now 100% complete and fully verified.**

---

## Contributing

Contributions are welcome! Here's how to get started:

1. **Fork** the repository and create a feature branch:
   ```bash
   git checkout -b feature/my-feature
   ```
2. **Make your changes** — follow Go conventions, add tests for new functionality.
3. **Run the tests** before submitting:
   ```bash
   go test ./...
   ```
4. **Open a Pull Request** against the `main` branch with a clear description of what you changed and why.

Please open an [issue](https://github.com/vyuvaraj/ServStore/issues) first for major feature proposals so we can discuss the design before implementation begins.

### Code Style
- Standard Go formatting (`gofmt` / `goimports`)
- All exported identifiers must have doc comments
- New packages must include at least one `_test.go` file

---

## License

Copyright © 2024 [vyuvaraj](https://github.com/vyuvaraj)

Licensed under the **Apache License, Version 2.0**. You may not use this project except in compliance with the License.

A copy of the license is included in this repository: [LICENSE](LICENSE)

```
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
```

---

## Use Without Servverse (Standalone Quickstart)

`ServStore` is fully standalone by default and functions as a standard, independent S3-compatible storage engine:
1. Run `ServStore`:
   ```bash
   ./servstore --port 8081 --dir ./data
   ```
2. Interact with it using any standard S3 client (e.g. `aws-cli` or `minio-go` SDK) by specifying `http://localhost:8081` as the endpoint:
   ```bash
   aws s3 mb s3://my-bucket --endpoint-url http://localhost:8081
   ```

---

<p align="center">
  Built with ❤️ in Go · <a href="https://github.com/vyuvaraj/ServStore">github.com/vyuvaraj/ServStore</a>
</p>

