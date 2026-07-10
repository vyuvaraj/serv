# ServCache

```bash
docker run -p 8084:8084 ghcr.io/vyuvaraj/servcache:latest
```

ServCache is the distributed, high-performance caching service for the Servverse ecosystem. It exposes a low-latency REST API backed by pluggable engines (in-memory or Redis) with native support for OpenTelemetry context propagation, read-through/write-behind database synchronisation, key pattern invalidation, and multi-region replication.

## Features

- **Pluggable Engines**: Swap transparently between thread-safe local in-memory storage and high-throughput Redis/Valkey clusters.
- **TTL Eviction**: Automatic, background time-based pruning of expired cache keys.
- **Key Pattern Invalidation**: Delete matching keys dynamically via wildcards and prefix matching.
- **Read-Through Cache**: Cache misses automatically load data from a backend database (`SERV_CACHE_BACKEND_DB`) and populate the cache.
- **Write-Behind Cache**: Writes asynchronously update the backend database in the background to ensure eventually consistent writes without blocking clients.
- **Multi-Region Replication**: Forward mutations asynchronously to peer cache nodes (`SERV_CACHE_PEERS`) to maintain global cache consistency.
- **OTel Instrumentation**: Standardized hit/miss/latency metrics automatically exported via OTel tracing context.

---

## API Endpoints

### 1. Health Checks
- `GET /health` - Health probe showing cache readiness and connection status.

### 2. Cache Operations

#### Set Cache Entry
* **Path**: `POST /api/cache`
* **Headers**: `Content-Type: application/json`
* **Body**:
  ```json
  {
    "key": "user:101",
    "value": { "name": "Alice", "role": "admin" },
    "ttl": "5m"
  }
  ```
  *(TTL uses standard Go duration strings like `10s`, `5m`, `1h`)*

#### Get Cache Entry
* **Path**: `GET /api/cache/{key}`
* **Response (200 OK)**:
  ```json
  {
    "key": "user:101",
    "value": { "name": "Alice", "role": "admin" }
  }
  ```
* **Response (404 Not Found)**: If key doesn't exist (and no database read-through is configured/succeeds).

#### Delete Cache Entry
* **Path**: `DELETE /api/cache/{key}`

#### Clear Cache / Invalidate Pattern
* **Path**: `DELETE /api/cache`
* **Query Parameters**:
  * `pattern` (Optional) - Wildcard pattern matching keys to delete (e.g. `user:*`). If omitted, fully clears the cache.
  * `replicated` (Internal) - Used by peer nodes to denote replication loops.

---

## Configuration (Environment Variables)

Configure ServCache dynamically by setting these parameters at startup:

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP Server port | `8088` |
| `REDIS_URL` | Redis cluster URL (e.g. `redis://localhost:6379`). Uses in-memory engine if unset. | *(In-Memory)* |
| `SERV_CACHE_BACKEND_DB` | Endpoint URL of the backend database for read-through & write-behind sync. | *(Disabled)* |
| `SERV_CACHE_PEERS` | Comma-separated URLs of peer ServCache nodes to replicate mutations (e.g. `http://peer1:8088,http://peer2:8088`). | *(Disabled)* |

---

## Running Locally

### 1. In-Memory Mode
```bash
go run main.go --addr :8088
```

### 2. Redis Mode
```bash
go run main.go --addr :8088 --redis-url redis://localhost:6379
```

### 3. Verification Suite
Run integration and unit tests:
```bash
go test -v ./...
```
