# Standard Library

Serv ships with 46 reusable modules in `stdlib/`. Import what you need:

```serv
import { ok, notFound } from "../stdlib/response.srv"
import { requireAuth } from "../stdlib/auth.srv"
```

## Quick Reference

### Security
| Module | Key Exports |
|--------|-------------|
| `auth.srv` | `bearerToken`, `basicAuth`, `requireAuth` |
| `crypto.srv` | `hashPassword`, `verifyPassword`, `randomToken`, `hmacSign` |
| `jwt.srv` | `jwtEncode`, `jwtDecode`, `jwtIsExpired` |
| `sanitize.srv` | `escapeHTML`, `stripTags`, `escapeSQL`, `sanitizeFilename` |
| `ratelimit.srv` | `createLimiter`, `isAllowed`, `remaining`, `resetLimiter` |
| `mask.srv` | `maskEmail`, `maskPhone`, `maskCard`, `maskString`, `redact` |
| `ip.srv` | `extractIP`, `isPrivate`, `isTrustedProxy`, `anonymizeIP` |

### HTTP
| Module | Key Exports |
|--------|-------------|
| `response.srv` | `ok`, `created`, `badRequest`, `notFound`, `serverError` |
| `pagination.srv` | `offset`, `pageResponse`, `parsePageParams` |
| `pagination_cursor.srv` | `encodeCursor`, `decodeCursor`, `cursorResponse` |
| `middleware.srv` | `corsHeaders`, `requestId`, `logRequest` |
| `http_client.srv` | `getJSON`, `postJSON`, `isSuccess`, `isClientError` |
| `url.srv` | `encodeURI`, `parseQuery`, `buildQuery`, `joinPath` |
| `cors.srv` | `allowOrigin`, `allowAll`, `preflightResponse` |

### Utilities
| Module | Key Exports |
|--------|-------------|
| `datetime.srv` | `now`, `timestamp`, `isExpired`, `formatDuration` |
| `strings_util.srv` | `slugify`, `truncate`, `capitalize`, `isEmpty` |
| `math.srv` | `min`, `max`, `clamp`, `abs`, `percent`, `sum`, `average` |
| `sort.srv` | `reverse`, `minOf`, `maxOf` |
| `collections.srv` | `unique`, `flatten`, `chunk`, `first`, `last`, `countWhere` |

### Data
| Module | Key Exports |
|--------|-------------|
| `csv.srv` | `parseCSV`, `parseRow`, `toCSV` |
| `base64.srv` | `encode`, `decode`, `isValid` |
| `diff.srv` | `hasChanged`, `fieldChanged`, `changeRecord` |

### Config
| Module | Key Exports |
|--------|-------------|
| `env.srv` | `requireEnv`, `envOrDefault`, `envInt`, `envBool` |
| `config.srv` | `getConfig`, `requireConfig`, `configBool`, `configList` |
| `feature_flags.srv` | `enableFlag`, `disableFlag`, `isEnabled`, `toggleFlag` |

### Resilience
| Module | Key Exports |
|--------|-------------|
| `retry.srv` | `backoffDelay`, `defaultMaxRetries` |
| `circuit_breaker.srv` | `createBreaker`, `isOpen`, `recordSuccess`, `recordFailure` |
| `timeout.srv` | `withDeadline`, `isTimedOut`, `remainingTime`, `elapsed` |
| `queue.srv` | `createQueue`, `enqueue`, `dequeue`, `queueSize` |

### Concurrency
| Module | Key Exports |
|--------|-------------|
| `semaphore.srv` | `createSemaphore`, `tryAcquire`, `release`, `available` |
| `batch.srv` | `createBatch`, `addToBatch`, `isBatchFull`, `flushBatch` |

### Processing
| Module | Key Exports |
|--------|-------------|
| `job.srv` | `createJob`, `startJob`, `completeJob`, `failJob` |
| `scheduler.srv` | `scheduleAfter`, `isScheduled`, `cancelSchedule` |

### Reliability
| Module | Key Exports |
|--------|-------------|
| `idempotency.srv` | `checkIdempotency`, `markProcessed`, `isProcessed` |
| `dlq.srv` | `createDLQ`, `sendToDLQ`, `dlqSize`, `clearDLQ` |

### Integration
| Module | Key Exports |
|--------|-------------|
| `webhook.srv` | `buildPayload`, `sendWebhook`, `verifySignature` |
| `events.srv` | `on`, `emit`, `hasHandler` |

### Observability
| Module | Key Exports |
|--------|-------------|
| `metrics.srv` | `counter`, `gauge`, `recordLatency`, `trackRequest` |
| `tracing.srv` | `traceId`, `startSpan`, `endSpan`, `traceContext` |

### Multi-tenancy
| Module | Key Exports |
|--------|-------------|
| `tenant.srv` | `extractTenant`, `tenantConfig`, `isTenantActive`, `tenantFilter` |

### Compliance
| Module | Key Exports |
|--------|-------------|
| `audit.srv` | `auditLog`, `auditAction`, `auditAccess`, `auditAuth`, `auditDenied` |

### Operations
| Module | Key Exports |
|--------|-------------|
| `health.srv` | `healthy`, `unhealthy`, `degraded`, `buildHealthResponse` |
| `graceful.srv` | `initShutdown`, `isShuttingDown`, `isDrained` |
| `cache_patterns.srv` | `cacheKey`, `cacheGet`, `cacheSet`, `invalidate`, `computeIfAbsent` |

### Testing
| Module | Key Exports |
|--------|-------------|
| `testing_helpers.srv` | `assertEqual`, `assertNotNil`, `assertContains`, `assertTrue` |

## Usage Example

```serv
import { requireAuth, bearerToken } from "../stdlib/auth.srv"
import { ok, badRequest } from "../stdlib/response.srv"
import { maskEmail } from "../stdlib/mask.srv"
import { auditLog } from "../stdlib/audit.srv"

server "8080"

route "GET" "/api/profile" (req) {
    let authErr = requireAuth(req)
    if authErr != nil { return authErr }

    let token = bearerToken(req)
    auditLog(token, "view", "profile", nil)

    return ok({
        "email": maskEmail("alice@example.com"),
        "role": "admin"
    })
}
```

Full module documentation: see comments at the top of each file in `stdlib/`.

## Deep-dive Module Examples

### 1. Resilience (`circuit_breaker.srv` & `retry.srv`)

Implement standard fault tolerance when requesting downstreams:

```serv
import { createBreaker, recordSuccess, recordFailure, isOpen } from "../stdlib/circuit_breaker.srv"
import { backoffDelay } from "../stdlib/retry.srv"

let breaker = createBreaker(3, 10s) // 3 failures, 10s timeout window

fn requestExternalAPI(url) {
    if isOpen(breaker) {
        return { "error": "circuit breaker open", "status": "failing" }
    }

    let res = http.get(url)
    if res.status != 200 {
        recordFailure(breaker)
        return nil
    }

    recordSuccess(breaker)
    return res.body
}
```

### 2. Concurrency (`semaphore.srv`)

Guard resources from over-concurrency:

```serv
import { createSemaphore, tryAcquire, release } from "../stdlib/semaphore.srv"

let sem = createSemaphore(5) // Max 5 parallel tasks

fn processTask(taskId) {
    if !tryAcquire(sem) {
        log.warn("Rate limited locally — too many concurrent workers")
        return false
    }
    defer release(sem)

    heavyComputation(taskId)
    return true
}
```

### 3. Masking & Compliance (`mask.srv` & `audit.srv`)

Sanitize user profiles and write logs:

```serv
import { maskEmail, maskCard } from "../stdlib/mask.srv"
import { auditLog } from "../stdlib/audit.srv"

fn getSafeProfile(req) {
    let email = req.body.email
    let card = req.body.card

    auditLog(req.user, "access", "billing-profile", nil)

    return {
        "email": maskEmail(email),
        "card": maskCard(card)
    }
}
```

