# ServMesh — Library-Level Service Mesh

ServMesh provides lightweight service discovery, client-side load balancing, automatic retries, and circuit breaking for the Serv ecosystem.

## Features

- **Dynamic Service Registry**: Control plane for registering active service instances.
- **Client-Side Load Balancing**: Round-robin distribution of requests to healthy backends.
- **Circuit Breaking**: Transitions states (`Closed`, `Open`, `Half-Open`) to avoid cascading failures.
- **Automatic Retries**: Backoff policies on transient timeouts.
- **OTel Trace Propagation**: Automatically forwards parent trace headers (`traceparent`) to downstreams.

## Getting Started

### Starting the Registry Control Plane

To launch the central service registry on port `8089`:

```bash
go run main.go --port 8089 --ttl 10s
```

### Registry API Endpoints

- `POST /api/register` - Registers a new instance
- `POST /api/heartbeat` - Refreshes instance TTL
- `GET /api/resolve/{service_name}` - Resolves a service to healthy endpoints

### Distributed Locking APIs

- `POST /api/lock/acquire` - Acquires a TTL lock for an owner
- `POST /api/lock/release` - Releases a held lock
- `POST /api/lock/extend` - Extends the TTL of a held lock
- `GET /api/lock/status?key=...` - Returns the status of a lock
- `GET /api/lock/list` - Returns all active locks

