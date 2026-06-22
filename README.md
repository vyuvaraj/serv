# ServCloud — Managed Deployment Platform & Orchestrator

ServCloud is the process orchestrator and hosting backend service for the Serv ecosystem. It mimics a managed PaaS, allowing developers to deploy services dynamically via HTTP APIs.

## Features

* **PaaS Deployment API**: Compile and run `.srv` background services on demand.
* **Process Isolation**: Allocates dedicated ports and tracks active process metrics.
* **Dynamic Gateway Routing**: Automatically registers running service endpoints with `ServGate`.
* **Telemetry & Logs**: Captures output/error pipes into a ring buffer for REST-based log streaming.
* **OTel Tracing**: Deep integration with the shared tracing system via `ServShared`.

---

## Getting Started

### Prerequisites

* Go 1.21 or higher
* (Optional) `serv` command-line compiler

### Running the Orchestrator

To start the ServCloud daemon:

```bash
go run main.go --port 8085 --workdir ./.deployments --gateway http://localhost:8080
```

### Options

```
  --port <port>        Port to listen on (default: 8085)
  --workdir <path>     Directory for deployments and builds (default: ./.deployments)
  --gateway <url>      ServGate endpoint for dynamic route sync (default: http://localhost:8080)
  --auth-token <val>   Auth token for Gateway registration (default: secret-token)
  --version            Print version information and exit
```

---

## API Endpoints

### 1. Deploy Service
* **Method**: `POST`
* **Path**: `/api/deploy`
* **Payload**:
  ```json
  {
    "name": "my-worker",
    "code": "server \"8080\" { route \"/\" -> \"OK\" }"
  }
  ```

### 2. List Services
* **Method**: `GET`
* **Path**: `/api/services`

### 3. Get Logs
* **Method**: `GET`
* **Path**: `/api/services/:name/logs`

### 4. Undeploy Service
* **Method**: `DELETE`
* **Path**: `/api/services/:name`
