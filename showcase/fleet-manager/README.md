# IoT Fleet Management & Telemetry System Showcase

This showcase demonstrates a production-grade **Distributed IoT Fleet Management & Telemetry System** that integrates all core components of the **Servverse** ecosystem:

1. **ServGate (API Gateway)**: Secure entrance proxying authenticated requests, rate-limiting API clients, and forwarding dashboard streams.
2. **Serv-lang (DSL)**: Compiles clean routing models, async sub/pub events, and scheduled maintenance rollups.
3. **ServQueue (Event Broker)**: Handles the high-throughput `telemetry.ingest` pipeline.
4. **ServStore (Storage)**: Records node states in SQLite and caches active sessions in Redis.
5. **ServMesh (Service Mesh)**: Provides service-to-service routing and mTLS tunnel protection.
6. **ServCloud (Orchestrator)**: Manages and deploys simulators and exporters.
7. **ServRegistry (Package Registry)**: Hosts and verifies signed firmware packages.

## Architecture

```
                       +-----------------------+
                       |   ServGate (Port 8080)|
                       +-----------+-----------+
                                   | (Reverse Proxy)
                                   v
                       +-----------+-----------+
                       | Fleet API (Port 4500) |
                       +-----+-----------+-----+
                             |           |
               +-------------+           +-------------+
               | (Pub)                                 | (Resolve)
               v                                       v
    +----------+----------+                 +----------+----------+
    |      ServQueue      |                 |       ServMesh      |
    | (telemetry.ingest)  |                 | (Service Discovery) |
    +----------+----------+                 +---------------------+
               |
               v (Sub)
    +----------+----------+                 +---------------------+
    |  Telemetry Workers  |                 |     ServRegistry    |
    | (Process & Save)    |                 |  (Firmware Packages)|
    +----------+----------+                 +---------------------+
               |
               v (Write)
    +----------+----------+
    |      ServStore      |
    | (SQLite / Redis)    |
    +---------------------+
```

## Running the Showcase

### 1. Start the Fleet API Service
```bash
serv run main.srv --watch
```

### 2. Start the ServGate API Gateway
```bash
servgate --config gateway.json
```

Open `http://localhost:8080/dashboard` in your browser to view the interactive real-time visualizer!
