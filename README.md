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

---

## Standalone Gateway Setup (Express / Flask / Spring Boot)

`ServGate` can act as a high-performance standalone API Gateway in front of non-ecosystem applications built with frameworks like Node.js (Express), Python (Flask), or Java (Spring Boot) without requiring any WebAssembly filters:

### 1. Express Backend Setup (Node.js)
To forward client requests dynamically from `ServGate` to an Express app listening on `:3000`:
* Configure a path-based routing rule in `config.json`:
  ```json
  {
    "prefix": "/api/users",
    "target": "http://localhost:3000"
  }
  ```
* Requests to `http://localhost:8080/api/users/profile` will be automatically stripped and routed to `http://localhost:3000/profile`.

### 2. Flask Backend Setup (Python)
For Flask APIs running on port `:5000`:
* Add the routing target rule to your `config.json` list:
  ```json
  {
    "prefix": "/api/predict",
    "target": "http://localhost:5000"
  }
  ```
* `ServGate` handles HTTP headers forwarding, logging, and connection pooling out-of-the-box.

### 3. Spring Boot Backend Setup (Java)
For Spring Boot controllers running on port `:8080` (or custom port `:8081`):
* Configure the target path mappings:
  ```json
  {
    "prefix": "/api/v1/billing",
    "target": "http://localhost:8081"
  }
  ```

### Complete Standalone `config.json` Example
Create a `config.json` file combining your multi-backend routing policies:
```json
{
  "addr": ":8080",
  "auth_token": "gateway-secret-token",
  "routes": [
    {
      "prefix": "/api/users",
      "target": "http://localhost:3000"
    },
    {
      "prefix": "/api/predict",
      "target": "http://localhost:5000"
    },
    {
      "prefix": "/api/v1/billing",
      "target": "http://localhost:8081"
    }
  ]
}
```

### Launch Standalone Gateway
Start the gateway pointing directly to your custom config file:
```bash
./servgate.exe --standalone --config config.json
```
The gateway is now running at `http://localhost:8080`, proxying requests dynamically based on prefixes!

