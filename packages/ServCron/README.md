# ServCron

```bash
docker run -p 8085:8085 ghcr.io/vyuvaraj/servcron:latest
```

ServCron is the distributed scheduling service for the Servverse ecosystem. It coordinates and executes scheduled tasks configured via `every` or `cron` declarations with distributed leader election, exactly-once scheduling semantics, and automatic S3-compatible persistence.

## Features

- **Interval & Cron Execution**: Run tasks periodically at fixed intervals (e.g. `10s`, `1m`) or standard 5-field cron patterns (e.g. `0 9 * * 1-5` for weekdays at 9 AM).
- **Dynamic Load Balancing**: Competes and distributes scheduled execution slots across active cluster nodes using Redis-based locks.
- **Persistent Job Registry**: Automatically persist and reload task definitions to/from a `ServStore` S3 bucket JSON schema (`jobs.json`) to prevent task definition losses during node crashes or restarts.
- **Job Execution Audit History**: Automatically records execution timestamps, duration metrics, response status codes, and response bodies, writing structured logs directly to S3 (`audit/<jobID>_<timestamp>.json`).
- **OTel Tracing Integration**: Emits client spans for job triggers and propagates `traceparent` headers to downstream callback requests for unified distributed tracing in `ServConsole`.

---

## API Endpoints

### 1. Health Checks
- `GET /healthz` - Health check.
- `GET /readyz` - Readiness check.
- `GET /health` - Detailed health status showing the node's current cluster role (`leader` or `follower`).

### 2. Jobs Administration
- `GET /api/jobs` or `GET /api/v1/jobs`
  * Lists all registered scheduled jobs.
- `POST /api/jobs` or `POST /api/v1/jobs`
  * Registers a new scheduled job.
  * Body structure:
    ```json
    {
      "id": "my-cron-job",
      "cron": "*/5 * * * *",
      "target_url": "http://localhost:8080/my-endpoint",
      "payload": "{\"test\":true}"
    }
    ```
- `DELETE /api/jobs/{id}` or `DELETE /api/v1/jobs/{id}`
  * Deletes a scheduled job from the registry.
- `POST /api/jobs/{id}/run` or `POST /api/v1/jobs/{id}/run`
  * Manually triggers job execution immediately.

---

## Configuration (Environment Variables)

Configure ServCron dynamically using these environment variables or fallback to JSON-manifest service discovery:

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | HTTP Server port | `8087` |
| `REDIS_URL` | Redis URL for distributed lease locks (e.g. `redis://localhost:6379`). Uses standalone election if unset. | *(Standalone)* |
| `REDIS_LOCK_KEY` | Lock key name in Redis | `servcron:leader:lock` |
| `REDIS_LEASE_DURATION` | Lock lease duration | `15s` |
| `SERV_STORE_ENDPOINT` | ServStore S3-compatible service URL. | `http://localhost:8081` |
| `SERV_STORE_BUCKET` | Dedicated S3 bucket for jobs and audit logs. | `serv-cron` |
| `SERV_STORE_AUTH_TOKEN` | Bearer token authorization secret for S3 writes. | `gateway-secret-token` |
| `SERVVERSE_DISCOVERY` | JSON discovery manifest mapping `store` endpoint and auth token. | *(Disabled)* |

---

## Running Locally

```bash
go run main.go --addr :8087 --redis-url redis://localhost:6379
```

### Verification Suite
Run integration and unit tests:
```bash
go test -v ./...
```
