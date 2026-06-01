# Regulatory Cron Service — Serv Port

A port of the Java regulatory-cron-service to Serv, demonstrating how a complex production
microservice maps to Serv's declarative syntax.

## Architecture Comparison

| Component | Java (Spring Boot) | Serv |
|-----------|-------------------|------|
| Scheduler | PriorityBlockingQueue + virtual thread loop | `every 1s` + `channel` + worker pool |
| Job execution | Spring bean resolution + Future.get(timeout) | `spawn` + `executeWithRetry()` |
| Distributed lock | MongoDB insert + DuplicateKeyException | `db.query("insert", "locks", ...)` |
| Lock heartbeat | Virtual thread + Thread.sleep | `spawn startHeartbeat()` + `time.sleep` |
| Retry logic | For loop + Thread.sleep(backoff) | For loop + `time.sleep(delay * 1000)` |
| REST API | @RestController + 12 endpoints | `route` declarations (12 endpoints) |
| DLQ | MongoDB + @Scheduled purge | `db.query` + `cron "0 0 2 * * *"` |
| Alerts | HttpClient + retry + rate limit | `http.post` + `cache.set` for rate limit |
| Metrics | Micrometer + Prometheus | `metric.inc()` + `/metrics` endpoint |
| Health | Spring Actuator | Auto-generated `/health` + `/ready` |
| Concurrency control | AtomicBoolean + ConcurrentHashMap | `atomic.new/inc/dec/get` |
| Worker pool | Executors.newVirtualThreadPerTaskExecutor | `spawn` + `channel.receive` loop |
| Middleware | Spring Security filter | `middleware xActor(req) { }` |
| Structured logging | Logback + Logstash JSON | `log.with()` + `LOG_FORMAT=json` |

## Line Count Comparison

| | Java | Serv |
|---|------|------|
| Domain models | ~150 lines (records + enums) | ~80 lines |
| Scheduler engine | ~250 lines | ~30 lines (in main.srv) |
| Job executor | ~200 lines | ~90 lines |
| REST API | ~150 lines | ~80 lines |
| DLQ service | ~120 lines | ~60 lines |
| Lock manager | ~80 lines | ~40 lines |
| Alert service | ~100 lines | ~40 lines |
| Config/boilerplate | ~300 lines (Spring config, security, datasource) | 0 lines |
| **Total** | **~1350 lines** | **~420 lines** |

**~70% reduction** in code while maintaining the same architecture and capabilities.

## What's Different

1. **No Spring framework** — Serv handles DI, HTTP, scheduling natively
2. **No build system config** — no Gradle, no dependency management
3. **No boilerplate** — no annotations, no config classes, no XML
4. **Channel-based scheduling** — replaces PriorityBlockingQueue with Go channels
5. **Single binary output** — `serv build` produces one executable

## What's Missing (vs Java version)

- Thymeleaf admin UI (would need a separate frontend)
- Optimistic concurrency with version field (could add with `db.upsert` + version check)
- Circuit breaker (Resilience4j) — would need a Serv equivalent
- Property-based tests (jqwik) — Serv test framework is simpler
- MongoDB connection resilience with in-memory buffer — partially covered by `let result, err = ...`

## Running

```bash
# Set environment
set MONGODB_URI=mongodb://localhost:27017/cron_service
set PORT=8086
set LOG_FORMAT=json

# Build and run
serv build examples/regulatory-cron/main.srv -o cron-service.exe
.\cron-service.exe
```

## API Endpoints

Same as the Java version:
- `GET /api/v1/jobs` — List jobs (paginated)
- `GET /api/v1/jobs/:id` — Get job details
- `POST /api/v1/jobs` — Create job
- `PUT /api/v1/jobs/:id` — Update job
- `DELETE /api/v1/jobs/:id` — Delete job
- `POST /api/v1/jobs/:id/pause` — Pause job
- `POST /api/v1/jobs/:id/resume` — Resume job
- `POST /api/v1/jobs/:id/trigger` — Manual trigger
- `GET /api/v1/jobs/:id/executions` — Execution history
- `POST /api/v1/jobs/bulk/pause` — Bulk pause
- `POST /api/v1/jobs/bulk/resume` — Bulk resume
- `GET /api/v1/dlq` — Dead Letter Queue
- `POST /api/v1/dlq/:id/replay` — Replay failed execution
- `GET /api/v1/stats` — Observability stats
- `GET /health` — Health check (auto)
- `GET /ready` — Readiness check (auto)
- `GET /metrics` — Prometheus metrics (auto)
