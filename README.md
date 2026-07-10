# ServGate

```bash
docker run -p 8080:8080 ghcr.io/vyuvaraj/servgate:latest
```

`ServGate` is a high-performance, programmable API Gateway and reverse proxy tailored for the **Serv** ecosystem. Its primary differentiating feature is **WASM-Powered Middleware**: the ability to execute sandboxed WebAssembly (WASI) modules inline on incoming request or response cycles to inspect, validate, or mutate payloads before forwarding.

---

## Key Features

* **Reverse Proxy**: Dynamic path-based routing (e.g. `/api/v1/orders/*` -> `http://127.0.0.1:8081`) with automatic URL prefix stripping.
* **WASM Inline Middleware**: Execute guest WASI filters to validate headers, manipulate query params, or enrich body payloads.
* **Distributed Observability**: Seamless trace propagation using OpenTelemetry standard headers (`traceparent`) and JSON-based OTLP span exports.
* **Authentication Security**: Build-in OAuth2 Bearer token checks for all routed backend targets.

---

## Quick Start

### 1. Build and Run
Ensure you have Go installed, then compile and run:
```bash
go build -o servgate.exe main.go
./servgate.exe
```
* The **API Gateway reverse proxy** listens on `:8080` (configured via `config.json`).
* Dynamic middleware registration is managed via `/api/admin/middleware/{name}`.

### 2. Configuration (`config.json`)
Manage routes declaratively:
```json
{
  "addr": ":8080",
  "auth_token": "gateway-secret-token",
  "routes": [
    {
      "prefix": "/api/v1/orders",
      "target": "http://127.0.0.1:8081",
      "middleware": "validator"
    }
  ]
}
```

### 3. Dynamic Middleware Registration
Register a compiled `.wasm` middleware:
```bash
curl -X POST http://localhost:8080/api/admin/middleware/validator \
  -H "Authorization: Bearer gateway-secret-token" \
  --data-binary @my_validator.wasm
```

---

## Verification

Run integration tests:
```bash
go test ./... -v
```
