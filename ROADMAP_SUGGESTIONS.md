# Serv-lang Roadmap Suggestions

Based on analysis of the existing roadmaps (`language-roadmap.md`, `next-roadmap.md`) and the current state of the codebase.

---

## Gaps in Current Roadmaps

The existing roadmaps cover language features and performance well, but are missing several categories that matter for real-world adoption.

---

## Suggested Additions

### Phase 12: Robust Error Model & Debugging

The current roadmaps don't address the debugging experience or error model completeness.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Stack traces in Serv | Map Go panics back to `.srv` line numbers using source maps | High |
| ✅ | `?` operator fix | Propagate actual error values, not just `nil` | High |
| ⬜ | Error types | `error { code: int, message: string }` — structured errors beyond strings | Medium |
| ⬜ | `finally` block | `try { } catch (e) { } finally { }` — guaranteed cleanup | Medium |
| ⬜ | Panic recovery context | Show which route/scheduler/subscriber triggered the panic | Medium |
| ⬜ | Debug mode | `serv run --debug` that emits verbose trace of execution flow | Low |

---

### Phase 13: Testing Maturity

Test framework exists but is basic. Production testing needs more.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Mocking framework | `mock http.get` / `mock db.query` — inject test doubles | High |
| ⬜ | Test isolation | Each test runs with fresh state (DB, cache, broker) | High |
| ⬜ | Integration test mode | `serv test --integration` — spin up real dependencies | Medium |
| ⬜ | Property-based testing | `test property "math" { assert add(a, b) == add(b, a) }` | Low |
| ⬜ | Snapshot testing | `assert response matches snapshot "api_response.json"` | Low |
| ⬜ | Test parallelism | Run tests concurrently for faster CI | Medium |
| ⬜ | Test filtering | `serv test file.srv --filter "user"` — run subset of tests | Medium |

---

### Phase 14: Multi-File Projects & Workspaces

The current model is single-file or flat imports. Real services have structure.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Project manifest | `serv.toml` or `serv.yml` defining entry point, dependencies, build config | High |
| ⬜ | Multi-file compilation | `serv build ./` compiles all `.srv` files in a directory | High |
| ⬜ | Dependency lockfile | `serv.lock` for reproducible builds | High |
| ⬜ | Workspace support | Monorepo with multiple services sharing stdlib/types | Medium |
| ⬜ | Private modules | Authentication for private package registries | Low |
| ⬜ | Vendoring | `serv vendor` — copy dependencies locally | Low |

---

### Phase 15: Security & Hardening

No mention of security in existing roadmaps.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | SQL injection prevention | Warn/error when string concatenation is used in `db.query()` | High |
| ⬜ | Secret management | `env.secret("DB_PASSWORD")` with masking in logs | High |
| ⬜ | CORS configuration | Declarative CORS: `cors { origins: ["https://app.com"] }` | Medium |
| ⬜ | Rate limit per-IP | Built-in IP-based throttling beyond route-level | Medium |
| ⬜ | Input sanitization | Auto-escape HTML/SQL in user inputs by default | Medium |
| ⬜ | Dependency scanning | `serv audit` — check for known vulnerabilities in Go deps | Low |

---

### Phase 16: Observability Depth

OTel and metrics exist, but real production observability needs more.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Distributed tracing propagation | Auto-inject/extract trace context across pub/sub and HTTP calls | High |
| ⬜ | Custom metric labels | `metric.inc("orders", { "region": "us-east" })` | Medium |
| ⬜ | Log correlation | Auto-attach trace ID to all log statements within a request | High |
| ⬜ | Performance profiling | `serv run --profile` generating pprof output | Medium |
| ⬜ | SLO definitions | `slo "p99 < 200ms" for "/api/users"` — built-in SLI tracking | Low |
| ⬜ | Error budgets | Track error rates and alert when budget is exhausted | Low |

---

### Phase 17: Deployment & Operations

Dockerize exists, but real deployment is more nuanced.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Multi-stage build optimization | Smaller Docker images with static linking | Medium |
| ⬜ | Environment profiles | `serv run --env production` loading env-specific config | High |
| ⬜ | Health check dependencies | `/ready` checks DB, cache, broker connectivity automatically | Medium |
| ⬜ | Kubernetes manifests | `serv k8s generate` — Deployment, Service, ConfigMap | Medium |
| ⬜ | Blue/green deploy support | Readiness gates with version-aware health checks | Low |
| ⬜ | Configuration hot-reload | Reload `config.yml` without restart | Low |

---

### Phase 18: Compiler Internals & Performance

From the performance analysis in `next-roadmap.md`, but missing execution plan.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Symbol table refactor | Replace `map[string]string` with scoped symbol table | High |
| ⬜ | Type inference engine | Proper unification, not just literal tracking | High |
| ⬜ | Incremental compilation | Only recompile changed files in multi-file projects | Medium |
| ⬜ | Dead code elimination | Don't emit unused functions in generated Go | Medium |
| ⬜ | Constant folding | `let x = 2 + 3` → `let x = 5` at compile time | Low |
| ⬜ | Compiler error recovery | Report multiple errors per file, not just the first | Medium |

---

### Phase 19: Ecosystem & Community

Missing entirely from current roadmaps.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Package registry (real) | Deploy `registry.serv-lang.org` or adopt Git-based packages | High |
| ⬜ | Starter templates | `serv init --template api`, `--template worker`, `--template scheduler` | Medium |
| ⬜ | Example gallery | Searchable collection of real-world service patterns | Medium |
| ⬜ | Contributing guide | How to add a language feature end-to-end | Medium |
| ⬜ | RFC process | Design docs for major language changes before implementation | Low |
| ⬜ | Benchmarks suite | Automated performance comparison vs. hand-written Go | Medium |
| ⬜ | Changelog automation | Generate changelog from commits for each release | Low |

---

### Phase 20: Advanced Language Features (Future)

Features that would differentiate Serv but aren't currently on any roadmap.

| Status | Item | Description | Priority |
|--------|------|-------------|----------|
| ⬜ | Structured concurrency | `spawn` with cancellation, timeouts, and join semantics | High |
| ⬜ | Pipeline operator | `data \|> transform \|> validate \|> save` | Medium |
| ⬜ | Pattern matching on types | `match value { int => ..., string => ... }` | Medium |
| ⬜ | Compile-time assertions | `static_assert sizeof(User) < 256` | Low |
| ⬜ | Decorator syntax | `@cached(60s) fn getUser(id) { ... }` | Low |
| ⬜ | Channels | `let ch = channel(10); ch.send(msg); let val = ch.receive()` | Medium |
| ⬜ | Traits/Mixins | Share behavior across structs without inheritance | Low |
| ⬜ | Sum types | `type Result = Ok(T) | Err(string)` — algebraic data types | Medium |

---

## Priority Summary

If I had to pick the top 10 items to add to the roadmap right now:

1. **Project manifest (`serv.toml`)** — multi-file projects are blocked without this
2. **Symbol table refactor** — prerequisite for real type safety
3. **Mocking framework** — testing production services needs this
4. **Stack traces in Serv** — debugging is currently painful
5. **Fix `?` operator** — error propagation is broken
6. **Log correlation with trace IDs** — observability baseline
7. **Package registry or Git-based deps** — ecosystem can't grow without it
8. **Environment profiles** — every production service needs this
9. **Compiler error recovery** — reporting one error at a time is painful
10. **Structured concurrency** — `spawn` without join/cancel is dangerous in production
