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
