# Changelog

## 3.0.4

### Added
- **Serv Test Explorer** — Sidebar panel in Explorer listing all `test "..."` blocks from every `.srv` file, grouped by file with collapse/expand. Refreshes on save.
- **serv bench panel** (`serv.runBench`) — Runs `serv bench <file>` in terminal and opens a live p50/p99/throughput results panel per route.
- **ServCloud Deployments** (`serv.viewDeployments`) — Live table of branch preview deployments with URLs, build status, and auto-refresh.
- **ServPool Inspector** (`serv.inspectPool`) — DB connection pool dashboard showing active/idle/max connections per named pool, with wait-queue alerts.
- **ServMail Queue** (`serv.inspectMail`) — Email queue dashboard showing queued/sent/bounced counts and per-item status with template names.

## 3.0.3

### Added
- **ServAuth Progressive Risk Scoring Dashboard** (`serv.inspectAuth`) tracing user devices, countries, and MFA step-ups.
- **Interactive REPL Launcher** (`serv.openREPL`) — Spawns a `serv repl` terminal inside VS Code for live expression evaluation without a full project build.
- **ServMesh Topology Viewer** (`serv.viewMesh`) — Renders a live Mermaid.js graph of all mesh service connections, with fallback static topology offline.
- **ServTrace Request Tracer** (`serv.traceRequests`) — Shows distributed trace spans with filterable trace ID, service, operation, latency, and OK/ERROR status. Auto-refreshes every 5s.
- **ServRegistry Health Monitor** (`serv.viewRegistry`) — Full table of all registered microservices with live health checks, ports, and uptime. Auto-refreshes every 4s.
- **Status Bar Health Indicator** — Persistent `$(circuit-board) Serv` item in the editor footer, clicking opens the Registry Monitor. Turns amber with service count when any service is down.

## 3.0.2

### Added
- **Visual DAG Flowchart Designer** (`serv.visualizeWorkflow`) rendering step sequences using Mermaid.js.
- **ServQueue Broker Explorer** (`serv.exploreQueue`) listing active partitions and consumer groups.
- **ServStore Bucket Manager** (`serv.exploreStore`) showing S3 directories.
- **ServLock Contention Dashboard** (`serv.exploreLocks`) tracing active lock waiters.
- **ServGate Route Simulator** (`serv.simulateRoute`) validating paths against config routes.
- **ServCron Scheduler Explorer** (`serv.exploreCron`) monitoring schedules and smart analysis warnings.
- **ServCache Stats Dashboard** (`serv.inspectCache`) displaying cache hit ratios.

## 3.0.1

### Fixed
- Colocated LSP path autodetection fixes and cross-platform terminal escaping corrections.

## 3.0.0

### Added
- Full LSP integration (diagnostics, autocomplete, hover, go-to-definition)
- Commands: Run, Build, Test, Watch with keybindings
- Format on Save support via `serv fmt`
- 30+ code snippets for common patterns
- Real-time diagnostics (type errors, unused variables, missing returns)
- Hover information for all symbols and built-in objects
- Editor title run button for `.srv` files

### Improved
- TextMate grammar extended for generics, optional types, union types
- Snippet coverage for all language features (MCP tools, migrations, WebSocket, etc.)

## 2.0.0

### Added
- Extended snippet library (structs, methods, error handling, middleware)
- Support for new language features (enums, generics, optional chaining)
- Configuration options for LSP and compiler paths

## 1.0.0

### Added
- Initial release
- TextMate syntax highlighting for `.srv` files
- Basic code snippets for routes, functions, and schedulers
- Extension icon and branding
