# CLI Reference

## serv build

Compile a `.srv` file to a native binary.

```bash
serv build <file.srv> [-o <output>]
```

**Examples:**
```bash
serv build app.srv                    # → service.exe
serv build app.srv -o myapp.exe       # Custom output name
```

## serv run

Compile and run immediately.

```bash
serv run <file.srv> [--watch]
```

**Options:**
- `--watch` — Watch for file changes and hot-reload

## serv test

Run tests defined in a `.srv` file.

```bash
serv test <file.srv>            # Run tests
serv test --cover <file.srv>    # Run tests with coverage report
```

Runs all `test "name" { ... }` blocks and reports results.

**With `--cover`:** Shows statement coverage percentage and saves a coverage profile to `.build/<hash>/coverage.out`.

## serv lint

Check syntax and perform static analysis without building.

```bash
serv lint <file.srv>
```

**Analysis includes:**
- Parse error detection with "did you mean?" suggestions
- Unused variable warnings
- Missing return detection for typed functions
- Type mismatch errors (wrong argument types/count)

**Exit codes:**
- `0` — No errors (may have warnings)
- `1` — Has parse errors or type errors

**Example output:**
```
  warning: variable 'unused' is declared but never used
   7 |     let unused = 42
            ^

  error: argument 1 of 'add' expects type 'int', got 'string'
   6 |     let result = add("hello", true)
                           ^

2 error(s), 1 warning(s)
```

## serv fmt

Format a `.srv` file (4-space indent, consistent style).

```bash
serv fmt <file.srv>            # Format in place
serv fmt --check <file.srv>    # Check only (exit 1 if unformatted)
```

## serv repl

Interactive Serv shell.

```bash
serv repl
```

**Commands inside REPL:**
- Type any expression to evaluate: `1 + 2`, `"hello".toUpper()`
- `let x = 42` — declare variables (persisted across lines)
- `state` — show all declarations
- `clear` — reset state
- `exit` — quit

## serv add

Generate a `.srv.d` declaration file for a Go package.

```bash
serv add <go-package-path>
```

**Examples:**
```bash
serv add github.com/google/uuid
serv add encoding/json
serv add net/url
```

Downloads the package (if needed) and generates type declarations in `declarations/`.

## serv packages

List installed package declarations.

```bash
serv packages
```

## serv remove

Remove a package declaration.

```bash
serv remove <package-name>
```

## serv install

Install a community package from ServRegistry and resolve its transitive dependencies.

```bash
serv install <package-name>
```

**Examples:**
```bash
serv install jwt
serv install retry
serv install pagination@1.2.0
```

Downloads the package tarball from the configured registry, extracts it to `packages/<name>/`, then reads its `serv.toml` `[dependencies]` section and recursively installs any missing transitive dependencies.

**Environment variables:**
- `SERV_REGISTRY` — Override the registry URL (default: `https://registry.serv-lang.org`)

**Output example:**
```
Downloading package from https://registry.serv-lang.org/packages/jwt.tar.gz...
✓ Package 'jwt' installed to packages/jwt/
  Resolving 2 dependencies...
  ↳ Installing dependency: crypto
  ✓ Package 'crypto' installed to packages/crypto/
  ↳ Installing dependency: base64
  • base64 (already installed)
```

## serv publish

Publish a package directory to ServRegistry.

```bash
serv publish <directory>
```

Creates a `.tar.gz` archive of the directory (which should contain a `serv.toml`) and uploads it to the configured registry. Requires `SERV_JWT_SECRET` environment variable for authentication.

## serv dockerize

Generate a Dockerfile for deployment.

```bash
serv dockerize <file.srv>
```

## serv migrate

Apply declarative `table` schema migrations to the database.

```bash
serv migrate [file-or-dir] [--db <connection-string>]
```

**Options:**
- `--db` — Database connection string. Falls back to `$DATABASE_URL` then `sqlite://serv.db`

**Supported connection strings:**

| Format | Example |
|--------|---------|
| SQLite | `sqlite://app.db` |
| PostgreSQL | `postgres://user:pass@localhost/mydb` |
| MySQL | `mysql://user:pass@localhost/mydb` |

**What it does:**

1. Scans `.srv` files for `table` declarations
2. Connects to the database
3. **Creates** tables that don't exist (`CREATE TABLE IF NOT EXISTS`)
4. **Adds** new columns to existing tables (`ALTER TABLE ADD COLUMN`)
5. Skips anything already up to date

**Example output:**
```
Found 3 table declaration(s):
  • users (5 columns)
  • posts (6 columns)
  • tags (2 columns)

  ✓ users: schema applied
  ✓ posts: schema applied
  - tags: already up to date

Migration complete: 2 table(s) created/updated.
```

## serv create

AI-scaffold a new `.srv` file from a natural language description.

```bash
serv create "<prompt describing your service>"
```

**Examples:**
```bash
serv create "a REST API for managing blog posts with SQLite"
serv create "a webhook receiver that processes Stripe payment events"
```

Requires `SERV_AI_KEY` environment variable (OpenAI or Gemini API key).

## serv dev

Start the full development environment with hot-reload and infrastructure services.

```bash
serv dev [file.srv] [--services all]
```

Starts ServDB, ServCache, ServQueue, and ServMesh locally, then watches `.srv`
files for changes and reloads the compiled service automatically.

## Runtime Flags

Compiled Serv binaries accept these flags:

```bash
./myservice.exe --port 9090     # Override server port
./myservice.exe --mcp           # Start as MCP tool server
```

**Environment variables:**
- `PORT` — Override server port
- `LOG_FORMAT=json` — JSON log output
- `LOG_LEVEL=debug` — Set log level
- `OTEL_ENDPOINT=http://localhost:4318` — Enable OpenTelemetry
- `OTEL_SERVICE_NAME=my-service` — Service name for traces
- `DATABASE_URL` — Default database connection string
- `SERV_MESH_ADDR` — ServMesh registry address (default: `http://localhost:8089`)
- `SERV_SELF_ADDR` — This service's advertised address for mesh registration
