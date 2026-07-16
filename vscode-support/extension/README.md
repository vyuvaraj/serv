# Serv Language Support for VS Code

Full IDE support for the [Serv programming language](https://github.com/vyuvaraj/Serv-lang) — build background services, APIs, and schedulers with a clean, expressive syntax that compiles to native binaries.

## Features

### Syntax Highlighting
Rich syntax coloring for all Serv constructs: routes, structs, functions, f-strings, type annotations, duration literals, and more.

### IntelliSense & Autocomplete
- **Smart completions** for keywords, built-in objects, and your own functions/structs
- **Snippet templates** for common patterns — type `route`, `struct`, `test`, `fn` and get full templates
- **Signature help** — parameter hints appear as you type function arguments
- **Import auto-organization** — type `use ` and press Tab to pick from all 18 stdlib modules with full API docs; a quick-fix lightbulb appears when you use `db.`, `cache.`, `http.` etc. without the corresponding `use` statement

### Inlay Type Hints _(v3.0.5+)_
Always-on inline hints show inferred return types on `fn` declarations (`→ string`) and inferred types on `let` bindings (`: int`, `: Result`, `: Response`). Toggle with the `serv.enableInlayHints` setting.

### Real-Time Diagnostics
Errors and warnings appear as you type:
- Parse errors with "did you mean?" suggestions
- Type mismatch errors (wrong argument types)
- Unused variable warnings
- Missing return detection
- Unreachable code detection

### Hover Information
Hover over any symbol to see its type signature — works on definitions, usages, and built-in objects like `log`, `db`, `cache`, `http`.

### Go to Definition
Jump to any function, struct, or variable definition. Works across files in your workspace.

### Format on Save
Automatic code formatting with 4-space indentation and consistent style — same as `serv fmt`.

---

## ServVerse Activity Bar Panel _(v3.0.6+)_

A dedicated **ServVerse** icon in the Activity Bar opens a live sidebar showing all 17 services with health status, port numbers, and uptime — polled from ServRegistry every 6 seconds. Falls back to mock data with an `offline` badge when the registry is unreachable. Use the ↺ refresh button in the panel title bar to force an update.

---

## Visual Dashboards & Explorers

Visual Webviews integrated directly into the workspace to observe and simulate local services:

| Command | Dashboard | Description |
|---------|-----------|-------------|
| `serv.visualizeWorkflow` | **Workflow DAG** | Live Mermaid.js flowchart of step sequences and compensating tasks |
| `serv.exploreQueue` | **ServQueue Broker** | Active topics, partition counts, and consumer group registrations |
| `serv.exploreStore` | **ServStore Bucket** | Object storage folders and file listings |
| `serv.exploreLocks` | **ServLock Contention** | Distributed locks, active leases, and FIFO waiter queues |
| `serv.simulateRoute` | **ServGate Router Simulator** | Simulates Gateway path-routing matches locally against the active config |
| `serv.exploreCron` | **ServCron Scheduler** | Scheduled cron jobs with overlap warnings |
| `serv.inspectCache` | **ServCache Inspector** | Real-time hit/miss metrics and active connection pool status |
| `serv.inspectAuth` | **ServAuth Risk Scoring** | Progressive auth sessions, device fingerprints, geo context, and MFA risk scores |
| `serv.openREPL` | **Interactive REPL** | Spawns a `serv repl` terminal for live expression evaluation |
| `serv.viewMesh` | **ServMesh Topology** | Live Mermaid.js graph of all mesh service connections |
| `serv.traceRequests` | **ServTrace Span Tracer** | Distributed trace spans with trace ID, service, latency, and OK/ERROR status. Auto-refreshes every 5s |
| `serv.viewRegistry` | **ServRegistry Monitor** | All registered microservices with live health checks, ports, and uptime. Auto-refreshes every 4s |
| `serv.runBench` | **Benchmark Panel** | Runs `serv bench` and shows p50/p99/throughput results per route |
| `serv.viewDeployments` | **Cloud Deployments** | Branch preview deployments with URLs and build status |
| `serv.inspectPool` | **ServPool Inspector** | DB connection pool stats (active/idle/max) with wait-queue alerts |
| `serv.inspectMail` | **ServMail Queue** | Email queue with queued/sent/bounced counts and per-item status |
| `serv.viewTunnels` | **ServTunnel Sessions** | Active tunnel sessions with client IP, target, protocol, duration, bytes in/out |

---

## Test Integration

### Serv Test Explorer _(v3.0.4+)_
A sidebar tree under the Explorer panel lists every `test "..."` block from all `.srv` files in the workspace, grouped by file. Refreshes automatically on save.

### Test Gutter Decorations _(v3.0.5+)_
Run `Serv: Run Tests (with Gutter Decorations)` to paint:
- 🟡 **yellow** dots on all test blocks as they run
- 🟢 **green** on passed tests
- 🔴 **red** on failed tests

Results persist when switching between tabs. The overview ruler is also colored per test. Use `Serv: Clear Test Gutter Markers` to reset.

---

## Commands

| Command | Keybinding | Description |
|---------|------------|-------------|
| `Serv: Run Current File` | `Ctrl+Shift+R` | Compile and run |
| `Serv: Build Current File` | `Ctrl+Shift+B` | Compile to binary |
| `Serv: Test Current File` | `Ctrl+Shift+T` | Run all tests |
| `Serv: Run in Watch Mode` | — | Hot-reload on changes |
| `Serv: Run Tests (with Gutter Decorations)` | — | Run tests and show pass/fail in editor gutter |
| `Serv: Clear Test Gutter Markers` | — | Clear all gutter decoration icons |
| `Serv: Add Missing Imports` | — | Auto-add all missing `use` statements |
| `Serv: Refresh Services Panel` | — | Force-refresh the Activity Bar services panel |

---

## Quick Start

1. Install the [Serv compiler](https://github.com/vyuvaraj/Serv-lang)
2. Install this extension
3. Create a new project:
   ```bash
   serv init my-api
   cd my-api
   serv run main.srv --watch
   ```
4. Open the folder in VS Code — you'll get full IDE support immediately

---

## Snippet Shortcuts

| Prefix | Expands to |
|--------|-----------|
| `service` | Full service scaffold with health check |
| `route` | HTTP route handler |
| `routeauth` | Route with middleware |
| `fn` | Function declaration |
| `fnt` | Typed function with return type |
| `struct` | Struct declaration |
| `method` | Method on a struct |
| `test` | Test block |
| `testtimeout` | Test with timeout |
| `beforeEach` | Setup block |
| `try` | Try-catch block |
| `letq` | Let with `?` error propagation |
| `leterr` | Multi-return error handling |
| `for` | For-in loop |
| `formap` | Map key-value iteration |
| `match` | Pattern matching |
| `import` | Stdlib import |
| `importgo` | Go package import |
| `dbquery` | Database query |
| `ws` | WebSocket handler |
| `every` | Interval scheduler |
| `cron` | Cron scheduler |
| `subscribe` | Pub/sub subscriber |
| `migration` | Database migration |
| `enum` | Enum declaration |
| `tool` | MCP tool definition |

---

## Language Highlights

```serv
server "8080"

use db
use cache
use http

struct User {
    name: string,
    email: string?,
    age: int
}

fn User.greet() -> string {
    return f"Hi, I'm {self.name}"
}

route "GET" "/users/:id" (req) use [auth] {
    let user = findUser(req.params.id)?
    return { "user": user.greet() }
}

every 5m {
    log.info("Cleaning expired sessions...")
    db.query("DELETE FROM sessions WHERE expires < ?", time.unix())
}

test "user greeting" {
    let u = User { name: "Alice", email: nil, age: 30 }
    assert u.greet() == "Hi, I'm Alice"
}
```

---

## Requirements

- [Serv compiler](https://github.com/vyuvaraj/Serv-lang) installed and in PATH
- Go 1.18+ (used by the compiler for code generation)

---

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `serv.lspPath` | `""` | Path to `serv-lsp` binary (auto-detected from PATH) |
| `serv.compilerPath` | `""` | Path to `serv` binary (auto-detected from PATH) |
| `serv.enableInlayHints` | `true` | Show inferred return type and variable type hints inline in the editor |

---

## Links

- [Language Reference](https://github.com/vyuvaraj/Serv-lang/blob/main/docs/language-reference.md)
- [Getting Started Guide](https://github.com/vyuvaraj/Serv-lang/blob/main/docs/getting-started.md)
- [Standard Library](https://github.com/vyuvaraj/Serv-lang/blob/main/docs/stdlib.md)
- [Examples](https://github.com/vyuvaraj/Serv-lang/tree/main/examples)
- [Report Issues](https://github.com/vyuvaraj/Serv-lang/issues)
- [Changelog](https://github.com/vyuvaraj/Serv-lang/blob/main/vscode-support/extension/CHANGELOG.md)
