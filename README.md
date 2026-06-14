# ServStore

A cloud-native, distributed-ready, S3-compatible object storage engine. ServStore serves as an open-source alternative to MinIO, designed for strong consistency, high scalability, and high performance.

Currently, this repository contains the **MVP release**, featuring a robust single-node storage engine with native S3 compatibility, Object Versioning, AWS Signature V4 verification, S3 Multipart Upload support, AES-256 Encryption-at-Rest, TLS 1.3 enforcement, a built-in Glassmorphic Admin Console, and lightweight OpenTelemetry tracing.

---

## Key Features
* **S3-Compatible REST API**: Native support for creating/deleting buckets, uploading/retrieving objects, HEAD requests, delete markers, and listing bucket contents.
* **S3 Multipart Uploads**: Supports standard S3 multipart operations (`InitiateMultipartUpload`, `UploadPart`, `CompleteMultipartUpload`, and `AbortMultipartUpload`) for uploading large files.
* **Object Versioning**: Supports versioning states (Enabled, Suspended, Disabled) matching AWS S3 versioning specs.
* **Authentication**: Decodes and verifies AWS Signature V4 (header-based and query-based signature verification).
* **AES-256 Encryption-at-Rest**: Optional per-object AES-256-GCM encryption of all stored data. Enabled via `--encryption-key`; passphrase is SHA-256 derived to a 32-byte key. Fully transparent to S3 clients.
* **TLS 1.3 Enforcement**: Optional HTTPS mode via `--tls-cert` / `--tls-key`. Forces TLS 1.3 minimum with preferred curves (X25519, P256). Gracefully falls back to HTTP when not configured.
* **OpenTelemetry Tracing**: A custom, lightweight, zero-dependency tracing client (inspired by the `Serv-lang` project) to export trace spans of HTTP routes and storage I/O operations to any OTel collector.
* **Console Dashboard**: A premium, responsive Web UI with dark mode, drag-and-drop uploads, bucket management, and version history viewer.
* **Single-Binary Deployment**: Frontend assets are embedded directly into the Go compiled binary for simple, zero-dependency distribution.
* **ServStore CLI (`servstore-cli`)**: A lightweight terminal client for managing buckets and objects — `mb`, `rb`, `ls`, `put`, `get`, `rm` commands targeting any ServStore endpoint.

---

## Directory Structure
```text
ServStore/
├── cmd/
│   ├── servstore/
│   │   └── main.go             # Server entry point, CLI flags, TLS & encryption config
│   └── servstore-cli/
│       └── main.go             # CLI client (mb, rb, ls, put, get, rm)
├── pkg/
│   ├── auth/
│   │   └── auth.go             # AWS Signature V4 authentication handler
│   ├── metrics/
│   │   ├── metrics.go          # Zero-dependency Prometheus metrics registry
│   │   └── metrics_test.go     # Unit tests for metrics serialisation
│   ├── otel/
│   │   ├── otel.go             # Lightweight OpenTelemetry tracing client
│   │   └── otel_test.go        # Unit tests for OTel tracing
│   ├── s3/
│   │   ├── api.go              # S3 API router, gateway handlers & HTTP tracing
│   │   └── xml.go              # S3-compliant XML request/response models
│   ├── storage/
│   │   ├── store.go            # Storage engine interface definition
│   │   ├── local_store.go      # Versioned storage, multipart staging & encryption hooks
│   │   ├── crypto.go           # AES-256-GCM encrypt/decrypt helpers
│   │   ├── crypto_test.go      # Unit tests for encryption round-trips
│   │   └── local_store_test.go # Storage engine test suite (including multipart tests)
│   └── web/
│       ├── server.go           # Web Console static asset and API router wrapper
│       └── assets/             # Web Console files (index.html, style.css, app.js)
├── roadmap.md                  # Phase-wise roadmap requirements
└── README.md                   # Product documentation
```

---

## Getting Started

### Prerequisites
* Go 1.22 or higher

### 1. Run Tests
Validate the versioned local storage engine and tracing modules by running the test suite:
```bash
go test -v ./...
```

### 2. Build the Server
Compile the single-binary executable:
```bash
go build -o servstore ./cmd/servstore
```

### 3. Run the Server
Launch the storage engine (by default it listens on port `8080` and stores data inside `./data` with authentication disabled for local console convenience):
```bash
./servstore
```

To run with AWS Signature V4 verification enabled:
```bash
./servstore --auth --access-key "yourAccessKey" --secret-key "yourSecretKey"
```

To enable AES-256 encryption at rest:
```bash
./servstore --encryption-key "my-strong-passphrase"
```

To enable HTTPS with TLS 1.3 (requires a PEM cert/key pair):
```bash
# Generate a self-signed cert for local testing
openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -days 365 -nodes -subj "/CN=localhost"

# Run with TLS enabled
./servstore --tls-cert ./server.crt --tls-key ./server.key
```

To enable OpenTelemetry tracing (e.g. exporting to a local OTel collector or Jaeger):
```bash
# Set OTel endpoint environment variables before running the binary
$env:OTEL_ENDPOINT="http://localhost:4318"
$env:OTEL_SERVICE_NAME="servstore"
./servstore
```

### 4. Open the Web Console
Navigate to [http://localhost:8080](http://localhost:8080) in your web browser. From here you can:
* Create and delete buckets.
* Toggle versioning (Enabled / Suspended).
* Drag and drop files to upload them.
* Inspect object metadata, download past versions, or permanently delete them.

---

## Roadmap

To see the development roadmap and requirements for building ServStore into a multi-node, Raft-replicated distributed system, see [roadmap.md](roadmap.md).
