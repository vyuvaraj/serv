# Changelog

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
