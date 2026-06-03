# Serv Standard Library

Reusable `.srv` modules for common service patterns. Import what you need:

```serv
import { ok, notFound } from "stdlib/response.srv"
import { requireAuth, bearerToken } from "stdlib/auth.srv"
```

---

## Module Index

| Module | Exports | Category |
|--------|---------|----------|
| `auth.srv` | `bearerToken`, `basicAuth`, `requireAuth` | Security |
| `crypto.srv` | `hashPassword`, `verifyPassword`, `randomToken`, `randomHex`, `hmacSign`, `hmacVerify` | Security |
| `jwt.srv` | `jwtEncode`, `jwtDecode`, `jwtIsExpired` | Security |
| `validation.srv` | `required`, `isEmail`, `isURL`, `minLength`, `maxLength`, `validateFields` | Input |
| `response.srv` | `ok`, `created`, `badRequest`, `notFound`, `serverError`, `errorResponse` | HTTP |
| `pagination.srv` | `offset`, `pageResponse`, `parsePageParams` | HTTP |
| `middleware.srv` | `corsHeaders`, `requestId`, `logRequest`, `isPreflight` | HTTP |
| `http_client.srv` | `getJSON`, `postJSON`, `isSuccess`, `isClientError`, `isServerError` | HTTP |
| `datetime.srv` | `now`, `timestamp`, `isExpired`, `formatDuration`, `sleep` | Utilities |
| `strings_util.srv` | `slugify`, `truncate`, `capitalize`, `isEmpty`, `repeat`, `matches` | Utilities |
| `env.srv` | `requireEnv`, `envOrDefault`, `envInt`, `envBool`, `envExists` | Config |
| `retry.srv` | `backoffDelay`, `defaultMaxRetries`, `defaultBaseDelay` | Resilience |
| `events.srv` | `on`, `emit`, `hasHandler` | Messaging |
| `collections.srv` | `groupBy`, `unique`, `flatten`, `chunk`, `first`, `last`, `countWhere` | Data |
| `testing_helpers.srv` | `assertEqual`, `assertNotNil`, `assertNil`, `assertContains`, `assertTrue`, `assertFalse`, `assertLength` | Testing |
| `health.srv` | `healthy`, `unhealthy`, `degraded`, `buildHealthResponse` | Ops |

---

## Categories

### Security
- **auth.srv** — Token extraction, bearer/basic auth, auth guards
- **crypto.srv** — Password hashing, HMAC signing, token generation
- **jwt.srv** — JWT encode/decode/expiry (lightweight; use `serv add github.com/golang-jwt/jwt/v5` for production)

### HTTP
- **response.srv** — Standard HTTP response builders (ok, notFound, etc.)
- **pagination.srv** — Page offset calculation, response envelope
- **middleware.srv** — CORS headers, request ID generation, preflight detection
- **http_client.srv** — JSON GET/POST wrappers, status code helpers

### Utilities
- **datetime.srv** — Timestamps, expiry checks, duration formatting
- **strings_util.srv** — Slugify, truncate, capitalize, pattern matching
- **collections.srv** — Array utilities (unique, flatten, chunk, first/last)

### Config & Environment
- **env.srv** — Required env vars, defaults, type coercion (int/bool)

### Resilience
- **retry.srv** — Exponential backoff calculation

### Messaging
- **events.srv** — In-process event bus (emit/on pattern)

### Testing
- **testing_helpers.srv** — Expressive assertions for test blocks

### Operations
- **health.srv** — Custom health check builders

---

## Usage Example

```serv
import { requireAuth, bearerToken } from "stdlib/auth.srv"
import { ok, badRequest } from "stdlib/response.srv"
import { required, isEmail } from "stdlib/validation.srv"
import { envOrDefault } from "stdlib/env.srv"

server envOrDefault("PORT", "8080")

route "POST" "/api/users" (req) {
    let authErr = requireAuth(req)
    if authErr != nil {
        return authErr
    }

    let errors = validate(req.body, {
        "email": "required,email",
        "name": "required"
    })
    if errors != nil {
        return badRequest(errors)
    }

    return ok({ "created": true })
}
```

---

## Contributing

Add new modules as `stdlib/<name>.srv`. Export functions with `export fn`. Follow existing patterns:
- Pure functions where possible
- No side effects unless explicitly documented
- Use `interface{}` params (no type annotations) for maximum flexibility
