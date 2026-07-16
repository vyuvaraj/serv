# Serv Language Support for VS Code

Full IDE support for the [Serv programming language](https://github.com/vyuvaraj/Serv-lang) — build background services, APIs, and schedulers with a clean, expressive syntax that compiles to native binaries.

## Features

### Syntax Highlighting
Rich syntax coloring for all Serv constructs: routes, structs, functions, f-strings, type annotations, duration literals, and more.

### IntelliSense & Autocomplete
- **Smart completions** for keywords, built-in objects, and your own functions/structs
- **Snippet templates** for common patterns — type `route`, `struct`, `test`, `fn` and get full templates
- **Signature help** — parameter hints appear as you type function arguments

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

### Visual Dashboards & Explorers
Visual Webviews integrated directly into the workspace to observe and simulate local services:
- **Visual DAG Flowchart** (`serv.visualizeWorkflow`) — Renders live interactive Mermaid.js flowcharts of step sequences and compensating tasks.
- **ServQueue Broker Explorer** (`serv.exploreQueue`) — Displays active queue broker topics, partition counts, and consumer group registrations.
- **ServStore Bucket Explorer** (`serv.exploreStore`) — Browses object storage folders and file listings.
- **ServLock Contention Dashboard** (`serv.exploreLocks`) — Debugs distributed locks, active leases, and FIFO waiter queues.
- **ServGate Router Simulator** (`serv.simulateRoute`) — Simulates Gateway path-routing matches locally using the active config file.
- **ServCron Scheduler Manager** (`serv.exploreCron`) — Lists scheduled cron jobs and prints warnings about schedule overlaps.
- **ServCache Performance Inspector** (`serv.inspectCache`) — Real-time hit/miss metrics and active connection pool status.
- **ServAuth Risk Scoring Dashboard** (`serv.inspectAuth`) — Visualizes progressive auth sessions, device fingerprints, geo context, and MFA risk scores per user.
- **Interactive REPL Launcher** (`serv.openREPL`) — Spawns a `serv repl` terminal inside VS Code for live expression evaluation without a full project build.
- **ServMesh Topology Viewer** (`serv.viewMesh`) — Renders a live Mermaid.js graph of all mesh service connections, falling back to default topology offline.
- **ServTrace Request Tracer** (`serv.traceRequests`) — Shows distributed trace spans with filterable trace ID, service, operation, latency, and OK/ERROR status. Auto-refreshes every 5s.
- **ServRegistry Health Monitor** (`serv.viewRegistry`) — Full table of all registered microservices with live health checks, ports, and uptime. Auto-refreshes every 4s.
- **Status Bar Health Indicator** — Always-visible `$(circuit-board) Serv` in the editor footer; turns amber when any service is down, click to open the Registry Monitor.

### Commands
- **Serv: Run Current File** (`Ctrl+Shift+R`) — compile and run
- **Serv: Build Current File** (`Ctrl+Shift+B`) — compile to binary
- **Serv: Test Current File** (`Ctrl+Shift+T`) — run tests
- **Serv: Run in Watch Mode** — hot-reload on changes

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

## Language Highlights

```serv
server "8080"

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

## Requirements

- [Serv compiler](https://github.com/vyuvaraj/Serv-lang) installed and in PATH
- Go 1.18+ (used by the compiler for code generation)

## Configuration

| Setting | Default | Description |
|---------|---------|-------------|
| `serv.lspPath` | `""` | Path to `serv-lsp` binary (auto-detected from PATH) |
| `serv.compilerPath` | `""` | Path to `serv` binary (auto-detected from PATH) |

## Links

- [Language Reference](https://github.com/vyuvaraj/Serv-lang/blob/main/docs/language-reference.md)
- [Getting Started Guide](https://github.com/vyuvaraj/Serv-lang/blob/main/docs/getting-started.md)
- [Standard Library](https://github.com/vyuvaraj/Serv-lang/blob/main/docs/stdlib.md)
- [Examples](https://github.com/vyuvaraj/Serv-lang/tree/main/examples)
- [Report Issues](https://github.com/vyuvaraj/Serv-lang/issues)
