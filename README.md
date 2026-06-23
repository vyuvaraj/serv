# ServCron

ServCron is the distributed scheduling service for the Servverse ecosystem. It executes scheduled tasks using `every` or `cron` declarations with distributed leader election and exactly-once execution semantics.

## Features

- **Interval Execution**: Set up jobs to execute at fixed time intervals (e.g. `10s`, `1m`, `2h`).
- **Distributed Leader Election**: Prevents duplicate executions by ensuring only one scheduler instance acts as the leader using Redis-based lease locking. Standalone leader fallback is used when Redis is not configured.
- **OTel Tracing**: Auto-injects `traceparent` context headers to incoming target executions, allowing end-to-end trace correlation in `ServTrace` and `ServConsole`.
- **REST Control APIs**: Manage scheduled jobs dynamically via JSON endpoints.

## API Endpoints

### 1. Health Checks
- `GET /healthz` - Health probe.
- `GET /readyz` - Readiness probe.
- `GET /health` - Detailed health and role status (returns `leader` or `follower`).

### 2. Jobs API
- `GET /api/jobs` or `GET /api/v1/jobs`
  - Returns a list of all currently registered scheduled jobs.
- `POST /api/jobs` or `POST /api/v1/jobs`
  - Registers a new scheduled job.
  - Body structure:
    ```json
    {
      "id": "my-job-id",
      "interval": "10s",
      "target_url": "http://localhost:8080/my-callback",
      "payload": "{\"some\":\"json\"}"
    }
    ```
- `DELETE /api/jobs/{id}` or `DELETE /api/v1/jobs/{id}`
  - Unregisters and stops a scheduled job.
- `POST /api/jobs/{id}/run` or `POST /api/v1/jobs/{id}/run`
  - Instantly triggers a job execution without waiting for its scheduled time.

## Configuration (Environment Variables)

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP Server port | `8087` |
| `REDIS_URL` | Redis URL for distributed lease locks (e.g. `redis://localhost:6379`) | *(Standalone)* |
| `REDIS_LOCK_KEY` | Lock key name in Redis | `servcron:leader:lock` |
| `REDIS_LEASE_DURATION` | Lock lease expiration duration | `15s` |

## Running Locally

```bash
go run main.go --addr :8087 --redis-url redis://localhost:6379
```
