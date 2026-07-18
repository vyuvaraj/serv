# Serv Language Reference

## Program Structure

A Serv program consists of top-level declarations and statements:

```serv
server "8080"                    // Infrastructure
database "sqlite://app.db"       // Database connection
cache "redis://localhost:6379"   // Cache connection
broker "nats://localhost:4222"   // Message broker

// Routes, functions, scheduled tasks, etc.
```

## Unified Application Block (`app`)

An `app` block acts as a namespace to group related servers, databases, and APIs within a single logical service boundary:

```serv
app GatewayService {
    server "8080"
    database "sqlite://app.db"

    export route "GET" "/health" (req) {
        return { "status": "UP" }
    }
}
```

## Variables

```serv
let name = "Alice"               // Type inferred
let age: int = 30                // Explicit type
let { x, y } = point            // Destructuring
let val, err = riskyFunction()   // Multi-return
```

## Types

| Type | Example |
|------|---------|
| `int` | `42` |
| `float` | `3.14` |
| `string` | `"hello"` |
| `bool` | `true`, `false` |
| `nil` | `nil` |
| `[]T` | `[1, 2, 3]` |
| `map` | `{ "key": "value" }` |
| `T?` | Optional (nullable) type |
| `T \| U` | Union type |

### Type Aliases

```serv
type UserID = int
type Email = string
```

### Optional Types (Null Safety)

Types suffixed with `?` allow `nil` values. Without `?`, assigning `nil` is a compile error.

```serv
let name: string = "Alice"     // Cannot be nil
let email: string? = nil       // OK — optional type

fn findUser(id: int) -> User? {
    let row = db.query("SELECT * FROM users WHERE id = ?", id)
    if row == nil { return nil }
    return User { name: row.name }
}
```

**Compile error example:**
```serv
let x: int = nil   // error: cannot assign nil to non-optional type 'int' (use 'int?' to allow nil)
```

### Union Types

Union types allow a value to be one of several types:

```serv
fn divide(a: int, b: int) -> int | error {
    if b == 0 {
        return "division by zero"
    }
    return a / b
}

fn process(input: string | int) {
    log.info(input)
}
```

## Functions

```serv
// Basic function
fn greet(name) {
    return f"Hello, {name}!"
}

// Typed parameters and return
fn add(a: int, b: int) -> int {
    return a + b
}

// Generic function
fn identity[T](value: T) -> T {
    return value
}

// Generic with constraints
fn max[T: Ordered](a: T, b: T) -> T {
    if a > b { return a }
    return b
}

// Arrow functions (closures)
let double = x => x * 2
let add = fn(a, b) { return a + b }

// Cached function (automatically checks/populates the runtime cache using parameterized keys)
cached fn getCachedConfig(key: string) -> string {
    return db.query("SELECT val FROM config WHERE key = ?", key)
}
```

### Generic Constraints

| Constraint | Supports |
|-----------|----------|
| `Comparable` | `==`, `!=` |
| `Ordered` | `<`, `>`, `<=`, `>=` |
| `Numeric` | `+`, `-`, `*`, `/` |
| `Integer` | Integer arithmetic |
| `Float` | Floating point |

## Control Flow

### If/Else

```serv
if condition {
    // ...
} else if other {
    // ...
} else {
    // ...
}
```

### For Loops

```serv
// Range-based
for item in items {
    log.info(item)
}

// Key-value iteration (maps)
for key, value in config {
    log.info(f"{key} = {value}")
}

// Condition-based
for count < 10 {
    count += 1
}
```

### Break & Continue

```serv
for item in items {
    if item == nil { continue }
    if item == "stop" { break }
    log.info(item)
}
```

### Match (Pattern Matching)

```serv
match status {
    "active" => { log.info("Active") }
    "inactive" => { log.info("Inactive") }
    _ => { log.info("Unknown") }
}
```

## Structs

```serv
struct User {
    name: string,
    email: string,
    age: int
}

// Methods
fn User.greet() -> string {
    return f"Hi, I'm {self.name}"
}

// Instantiation
let user = User { name: "Alice", email: "a@test.com", age: 30 }
log.info(user.greet())
```

## Enums

```serv
// Simple (string values)
enum Color { Red, Green, Blue }

// With explicit values
enum HttpStatus {
    OK = 200,
    NotFound = 404,
    ServerError = 500
}
```

## Interfaces

```serv
interface Serializable {
    fn serialize() -> string
    fn deserialize(data: string)
}
```

## HTTP Routes

```serv
route "GET" "/users" (req) {
    return { "users": [] }
}

route "POST" "/users" (req) {
    let body = req.body
    return { "created": true }
}

// With rate limiting
route "GET" "/api/data" (req) limit 100/minute {
    return { "data": "limited" }
}

// With middleware
route "GET" "/protected" (req) use [auth, logging] {
    return { "secret": "data" }
}

// With return contract validation (verifies response type matches User struct at build time)
route "GET" "/user" (req) -> User {
    let u = User { name: "Alice" }
    return u
}
```

### Request Object

| Field | Type | Description |
|-------|------|-------------|
| `req.body` | string | Request body (JSON string) |
| `req.method` | string | HTTP method |
| `req.path` | string | URL path |
| `req.params` | map | URL params + headers |

## WebSockets

```serv
ws "/chat" (conn) {
    for true {
        let msg = conn.receive()
        if msg == nil { break }
        conn.send(f"Echo: {msg}")
    }
}
```

## Scheduled Tasks

```serv
// Fixed interval
every 5s {
    log.info("Tick")
}

// Cron expression
cron "0 0 * * *" {
    log.info("Midnight job")
}
```

## Pub/Sub Messaging

```serv
// Subscribe to a topic
subscribe "orders.new" (msg) {
    log.info("New order: ", msg)
}

// Publish a message
publish "notifications" "Order confirmed"
```

## Concurrency

```serv
// Fire and forget
spawn processOrder(order)

// With worker pool limit
spawn(5) heavyTask(data)

// Async/await
let result = await fetchData()
let all = await all([task1(), task2(), task3()])
```

## Error Handling

```serv
// Try/catch (traditional)
try {
    let result = http.get("http://api.example.com/data")
    log.info(result.body)
} catch (err) {
    log.error("Failed: ", err)
}

// Multi-return error handling
let data, err = riskyCall()
if err != nil {
    log.error(err)
}

// ? operator — early return on error (recommended)
fn loadUser(id: int) -> User? {
    let row = db.query("SELECT * FROM users WHERE id = ?", id)?
    let parsed = json.parse(row)?
    return User { name: parsed.name }
}
```

The `?` operator calls the expression and:
- If it returns `nil` or an error, returns `nil` from the enclosing function
- If it succeeds, unwraps the value and continues

## Middleware

```serv
middleware auth(req) {
    let token = req.params.authorization
    if token == nil {
        return { "error": "Unauthorized", "status": 401 }
    }
}

route "GET" "/protected" (req) use [auth] {
    return { "data": "secret" }
}
```

## Optional Chaining

```serv
let city = user?.address?.city    // nil if any part is nil
```

## Spread Operator

```serv
let defaults = { "timeout": 30, "retries": 3 }
let config = { ...defaults, "timeout": 60 }
```

## Operators

### Arithmetic

| Operator | Description | Example |
|----------|-------------|---------|
| `+` | Addition / concatenation | `a + b` |
| `-` | Subtraction | `a - b` |
| `*` | Multiplication | `a * b` |
| `/` | Division | `a / b` |
| `%` | Modulo (remainder) | `a % b` |

### Compound Assignment

```serv
let count = 0
count += 1       // count = count + 1
count -= 1       // count = count - 1
count *= 2       // count = count * 2
count /= 2       // count = count / 2
count %= 3       // count = count % 3
```

### Bitwise Operators

| Operator | Description | Example |
|----------|-------------|---------|
| `&` | Bitwise AND | `a & b` |
| `\|` | Bitwise OR | `a \| b` |
| `^` | Bitwise XOR | `a ^ b` |
| `<<` | Left shift | `a << 2` |
| `>>` | Right shift | `a >> 1` |

### Comparison

| Operator | Description |
|----------|-------------|
| `==` | Equal |
| `!=` | Not equal |
| `<` | Less than |
| `>` | Greater than |
| `<=` | Less than or equal |
| `>=` | Greater than or equal |

### Logical

| Operator | Description |
|----------|-------------|
| `and` | Logical AND |
| `or` | Logical OR |
| `!` | Logical NOT |

## Slice Expressions

```serv
let items = [1, 2, 3, 4, 5]
let first3 = items[0:3]     // [1, 2, 3]
let rest = items[2:]         // [3, 4, 5]
let head = items[:2]         // [1, 2]

let text = "hello world"
let sub = text[0:5]          // "hello"
```

## Imports & Modules

```serv
// Import a local .srv module (relative path)
import "models/user.srv"
import { User, Role } from "models/user.srv"

// Import from stdlib (no relative path needed)
import { ok, notFound } from "stdlib/response"
import { requireAuth } from "stdlib/auth"
import { hashPassword } from "stdlib/crypto"

// Import a Go package
import uuid from "github.com/google/uuid"
let id = uuid.New()

// .srv extension is optional for stdlib imports
import { maskEmail } from "stdlib/mask.srv"   // also works
```

**Import resolution order:**
1. `stdlib/X` — resolved from project root's `stdlib/` directory
2. `./path` or `../path` — resolved relative to the importing file
3. Bare path — resolved relative to the importing file

## External Function Bindings

```serv
// Go package
extern fn generateID() from "go:github.com/google/uuid:NewString"

// Python script
extern fn analyze(data) from "python:./scripts/analyzer.py:analyze"
```

## Testing

```serv
test "math works" {
    let result = add(2, 3)
    assert result == 5          // "got X, want 5" on failure
}

test "comparisons" {
    assert 10 > 5               // "10 is not > 5" on failure
    assert "hello" != "world"   // "expected value to not equal world" on failure
}

test "string methods" {
    assert "hello".toUpper() == "HELLO"
    assert "  hi  ".trim() == "hi"
}
```

**Assertion messages:**
- `assert x == 5` → `assertion failed: got 3, want 5`
- `assert x != 0` → `assertion failed: expected value to not equal 0`
- `assert x > 10` → `assertion failed: 5 is not > 10`
- `assert valid` → `assertion failed: expected truthy value, got false`

## Config Validation

```serv
validate {
    required "db.host",
    required "db.port",
    optional "log.level"
}
```

## Request Validation

```serv
let errors = validate(req.body, {
    "email": "required,email",
    "name": "required,string",
    "age": "int"
})
```

## Declarative Schema Migrations (`table`)

Declare your database schema natively in `.srv` files. The compiler generates
the SQL automatically; `serv migrate` applies it to the live database.

```serv
table users {
    id        int      @primary @autoincrement
    name      string   @required
    email     string   @unique
    role      string   @default(user)
    createdAt datetime @default(now)
}

table posts {
    id        int      @primary @autoincrement
    userId    int      @required
    title     string   @required
    body      string
    published bool     @default(0)
    createdAt datetime @default(now)
}
```

### Column Annotations

| Annotation | SQL equivalent | Notes |
|-----------|----------------|-------|
| `@primary` | `PRIMARY KEY` | Mark as primary key |
| `@autoincrement` | `AUTOINCREMENT` | Auto-increment integer (SQLite) |
| `@required` | `NOT NULL` | Field cannot be null |
| `@unique` | `UNIQUE` | Enforce unique constraint |
| `@default(value)` | `DEFAULT value` | Set default; use `now` for `CURRENT_TIMESTAMP` |

### Serv → SQL Type Mapping

| Serv type | SQL type |
|-----------|----------|
| `int` | `INTEGER` |
| `float` | `REAL` |
| `bool` | `INTEGER` (0/1) |
| `string` | `TEXT` |
| `datetime` | `DATETIME` |

### `serv migrate` workflow

```bash
# Apply all table declarations to the database (default: sqlite://serv.db)
serv migrate

# Target a specific file or directory
serv migrate ./schemas/

# Override the database connection
serv migrate --db sqlite://production.db
serv migrate --db postgres://user:pass@localhost/mydb
```

`serv migrate` will:
- **Create** tables that don't exist yet (`CREATE TABLE IF NOT EXISTS`)
- **Add** missing columns to existing tables (`ALTER TABLE ADD COLUMN`)
- Skip tables/columns that are already up to date

> **Note:** Column renames and type changes require a manual migration block (see below).

### Raw SQL migrations (legacy / advanced)

For custom logic, constraints, or renaming operations use the `migration` block:

```serv
migration "add_users_index" {
    db.query("CREATE INDEX idx_users_email ON users (email)")
}

migration "rename_status_column" {
    db.query("ALTER TABLE orders RENAME COLUMN status TO order_status")
}
```

Raw migrations are applied in declaration order and tracked in `schema_migrations`.

## MCP Tools

```serv
tool "calculator" "Performs math operations" (args) {
    let result = args.a + args.b
    return { "result": result }
}
```

## AI Agents (`agent`)

Declare autonomous AI agents with system prompts, model routing, and tool bindings:

```serv
agent SupportBot {
    system "You are a helpful customer support assistant."
    model  "openai://gpt-4o"
    tools  ["lookup_order", "create_ticket"]
}

tool "lookup_order" "Look up an order by ID" (args) {
    let row = db.query("SELECT * FROM orders WHERE id = ?", args.order_id)
    return row
}
```

**Supported model URI schemes:**
- `openai://gpt-4o` — OpenAI GPT-4
- `anthropic://claude-3-5-sonnet` — Anthropic Claude
- `google://gemini-2.0-flash` — Google Gemini
- `local://ollama/llama3` — Local Ollama model

**Agent configuration keys:**

| Key | Description |
|-----|-------------|
| `system` | System prompt / instruction |
| `model` | Model URI |
| `tools` | List of `tool` block names available to the agent |

## Native Infrastructure & Component Keywords

### 1. Storage Buckets (`bucket`)
Bind and interact with ServStore S3 storage natively:
```serv
bucket media {
    path "servstore://media-bucket"
    allowed_types ["image/jpeg", "image/png", "application/pdf"]
}

// Upload file payload
media.put("user_1_avatar.png", req.body.file)
```

### 2. Retrieval-Augmented Generation (`RAG`)
Declare semantic index and document query stores directly:
```serv
rag DocumentIndex {
    source "servstore://docs"
    embed "openai"
    chunk 512
}

// Query RAG context inside AI agent chat
let context = DocumentIndex.query("How to setup mTLS?")
let reply = ai.chat(context)
```

### 3. Distributed Mutual Exclusion (`lock` block)
Scope-level locking backed by the ServLock coordinator:
```serv
lock "billing:invoice:42" {
    // Critical Section: Only one instance runs this at a time
    processInvoice(42)
} // Automatic deferred release on block exit
```

### 4. Event Handler (`on` block)
Subscribe to event queues via NATS / ServQueue brokers:
```serv
on "user.signup" (event) {
    log.info("New signup recorded: ", event.email)
    sendWelcomeEmail(event.email)
}

## Environment Variables & Secrets Lookup

Read environment variables and secure secrets dynamically:

```serv
let port = env("PORT")                   // Standard env lookup
let dbPassword = env.secret("DB_PASS")  // Safe retrieval from configured secret manager
```

## Inline Go Integration (`@inline go`)

Write raw Go functions directly inside `.srv` source files. This provides an escape hatch for raw performance or utilizing package features that aren't fully wrapped by the compiler yet:

```serv
@inline go fn sha256sum(input string) string {
    import "crypto/sha256"
    import "encoding/hex"

    h := sha256.New()
    h.Write([]byte(input))
    return hex.EncodeToString(h.Sum(nil))
}

test "test inline Go code" {
    let hash = sha256sum("hello")
    assert hash == "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
}
```

*Note: Any `import` statements declared inside the `@inline go` block are automatically hoisted by the compiler and added as top-level package imports in the compiled Go artifact.*

## Built-in Utility Namespaces

Phase 35 introduces native namespaces for shell execution and direct file system access:

### 1. Shell Command Execution (`exec` namespace)
Runs a shell command and returns output streams and exit codes:

```serv
let result = exec.run("echo 'hello world'")
assert result.exitCode == 0
assert result.stdout.trim() == "hello world"
```

- **`exec.run(commandString)`**: Runs command under `powershell` (Windows) or `sh` (Linux). Returns a map: `{ stdout: string, stderr: string, exitCode: int }`.

### 2. Direct File I/O (`file` namespace)
Enables reading and writing files directly from the filesystem without registering a `store` block:

```serv
let tempFile = "./log.txt"
file.write(tempFile, "Servverse success")

if file.exists(tempFile) {
    let contents = file.read(tempFile)
    log.info("File contents: " + contents)
}

let allFiles = file.list(".")
```

- **`file.read(path)`**: Reads whole file to string.
- **`file.write(path, content)`**: Writes string content to a file (creates if not exists, overwrites).
- **`file.exists(path)`**: Returns `true` if file/directory exists, `false` otherwise.
- **`file.list(path)`**: Returns an array of file/folder names inside the directory.

### 3. CSV Parsing (`csv` namespace)
- **`csv.parse(content)`**: Parses raw CSV string to a matrix array of rows.
- **`csv.stringify(rows, headers)`**: Serializes array of rows and optional headers to a CSV formatted string.

### 4. XML Serialization (`xml` namespace)
- **`xml.parse(content)`**: Parses XML string into a nested map structure.
- **`xml.stringify(obj)`**: Converts map/slice structure into XML string.

### 5. YAML Serialization (`yaml` namespace)
- **`yaml.parse(content)`**: Parses YAML string to nested maps/lists.
- **`yaml.stringify(obj)`**: Serializes maps/lists to YAML format string.

### 6. Path Helpers (`path` namespace)
- **`path.join(args...)`**: Joins path components.
- **`path.dirname(p)`**: Returns directory part.
- **`path.basename(p)`**: Returns last component.
- **`path.ext(p)`**: Returns extension.
- **`path.abs(p)`**: Returns absolute path.

### 7. Regular Expressions (`regex` namespace)
- **`regex.match(pattern, val)`**: Returns `true` if regex matches the string.
- **`regex.find(pattern, val)`**: Returns first match text.
- **`regex.replace(pattern, val, repl)`**: Replaces pattern matches.

### 8. Math Utilities (`math` namespace)
- **`math.floor(x)`**, **`math.ceil(x)`**, **`math.round(x)`**, **`math.abs(x)`**, **`math.pow(base, exp)`**, **`math.sqrt(x)`**: Basic floating-point operations.
- **`math.min(a, b)`**, **`math.max(a, b)`**: Min/max values of two numbers.

### 9. Encoding (`encoding` namespace)
- **`encoding.base64.encode(str)`** / **`encoding.base64.decode(str)`**: Base64 conversions.
- **`encoding.hex.encode(str)`** / **`encoding.hex.decode(str)`**: Hexadecimal conversions.

### 10. Hashing (`hash` namespace)
- **`hash.md5(str)`**, **`hash.sha256(str)`**, **`hash.sha512(str)`**: Cryptographic digests.
- **`hash.hmac(key, data, algo)`**: Generates HMAC signature.

### 11. UUID Generation (`uuid` namespace)
- **`uuid.v4()`**: Generates random UUID v4.
- **`uuid.v7()`**: Generates time-ordered UUID v7.

### 12. Randomness (`rand` namespace)
- **`rand.int(min, max)`**: Secure random integer in the range.
- **`rand.float()`**: Secure random float in `[0.0, 1.0)`.
- **`rand.string(n)`**: Secure random alphanumeric string of length `n`.
- **`rand.bool()`**: Secure random boolean.

### 13. URL Utilities (`url` namespace)
- **`url.parse(urlStr)`**: Parses URL to map of scheme, host, path, query.
- **`url.encode(str)`** / **`url.decode(str)`**: Escapes/unescapes URL components.

### 14. Environment Variables (`env` namespace)
- **`env.get(key)`**: Reads environment variable.
- **`env.require(key)`**: Reads environment variable or panics if empty/unset.
- **`env.int(key, default)`**: Reads environment variable parsed as integer.
- **`env.bool(key, default)`**: Reads environment variable parsed as boolean.

## Optional Chaining (`?.`)

Enables safe member traversal. If any parent in the path resolves to `nil`, the entire chain short-circuits to `nil` rather than throwing a panic:

```serv
let city = user?.address?.city
// If user or user.address is nil, city is set to nil safely
```

## Spread Operator (`...`)

Merge arrays and maps efficiently using the spread operator syntax:

```serv
let baseArr = [1, 2]
let combined = [...baseArr, 3, 4] // [1, 2, 3, 4]

let config = { timeout: 30 }
let customConfig = { ...config, retries: 3 } // { timeout: 30, retries: 3 }
```

## Time Namespace Overhaul (`time` namespace)

The `time` namespace has been extended to provide complete parsing, formatting, TZ translation, comparisons, and part-destructuring:

- **`time.parse(str, layout)`**: Parses string representation using specific layout.
- **`time.format(t, layout)`**: Formats `time` value back to string.
- **`time.add(t, duration)`**: Adds human-readable duration (e.g. `"24h"`, `"30m"`).
- **`time.sub(t1, t2)`**: Computes difference in seconds (returns `float64`).
- **`time.before(t1, t2)`** / **`time.after(t1, t2)`**: Time value comparisons.
- **`time.inZone(t, timezone)`**: Translates `time` value to IANA timezone location (e.g. `"America/New_York"`).
- **`time.utc(t)`** / **`time.local(t)`**: Shorthands to translate time location to UTC or Local.
- **`time.components(t)`**: Destructures a `time` value into its parts: `{ year, month, day, hour, minute, second, weekday, tz }`.
- **`time.fromUnix(seconds)`**: Converts Unix epoch timestamp back to a time value.

### Predefined Layout Constants
- **`time.RFC3339`**: RFC3339 layout (`"2006-01-02T15:04:05Z07:00"`)
- **`time.DATE`**: Date layout (`"2006-01-02"`)
- **`time.DATETIME`**: Datetime layout (`"2006-01-02 15:04:05"`)
- **`time.TIME`**: Clock time layout (`"15:04:05"`)
- **`time.HTTP`**: HTTP header layout (`"Mon, 02 Jan 2006 15:04:05 MST"`)

## Multiline String Dedentation

Backtick raw string literals are automatically dedented at compile-time by stripping the common leading whitespace prefix from each line:

```serv
let query = `
    SELECT *
    FROM users
    WHERE status = 'active'
`
// The common prefix of 4 spaces is automatically stripped from all lines
```## JWT Namespace (`jwt`)

Allows encoding, decoding, and verification of JSON Web Tokens natively:

- **`jwt.sign(payload map, secret string) string`**: Generates a signed HS256 JWT token.
- **`jwt.verify(token string, secret string) map`**: Decodes and verifies the signature and expiration of a token, returning claims.
- **`jwt.decode(token string) map`**: Decodes a token without verifying its signature, returning claims.

```serv
let token = jwt.sign({ "sub": "alice", "admin": true }, "my-secret")
let claims = jwt.verify(token, "my-secret")
```

## Compression Namespace (`compress`)

Provides compression and decompression utilities:

- **`compress.gzip(data)`**: Gzips string or binary data, returning compressed bytes.
- **`compress.ungzip(bytes)`**: Decompresses gzip bytes, returning a string.
- **`compress.deflate(data)`**: Deflates string or binary data, returning compressed bytes.
- **`compress.inflate(bytes)`**: Decompresses deflate bytes, returning a string.

```serv
let compressed = compress.gzip("hello world")
let original = compress.ungzip(compressed)
```

## Semantic Versioning Namespace (`semver`)

Parses and validates semantic version strings:

- **`semver.parse(version string) map`**: Parses version into `{ major, minor, patch }` map.
- **`semver.compare(v1 string, v2 string) int`**: Compares two versions; returns `-1` if `v1 < v2`, `0` if `v1 == v2`, `1` if `v1 > v2`.
- **`semver.satisfies(range string, version string) bool`**: Checks if version satisfies range constraints (e.g. `^1.2.3`, `~1.2.3`, `>=2.0.0`).

```serv
let ok = semver.satisfies("^1.2.3", "1.5.0")
```

## Duration Namespace (`duration`)

Simplifies operations on human-readable time spans:

- **`duration.parse(dur string) float`**: Parses duration strings (like `"2h30m"`) to float seconds.
- **`duration.format(seconds float) string`**: Formats seconds to a standard duration string (like `"2h30m0s"`).
- **`duration.since(ts time) float`**: Returns seconds elapsed since a past timestamp.

```serv
let secs = duration.parse("1h15m")
```

## Value Formatting Namespace (`format`)

Provides human-readable value formatting:

- **`format.bytes(size int) string`**: Formats byte count to string (e.g. `1048576` -> `"1 MB"`).
- **`format.number(num float) string`**: Formats large numbers to shorthand (e.g. `1500000` -> `"1.5M"`).
- **`format.percent(val float) string`**: Formats fraction to percentage (e.g. `0.856` -> `"85.6%"`).
- **`format.plural(count float, singular string, plural string) string`**: Formats count with noun (e.g. `1` -> `"1 item"`, `5` -> `"5 items"`).

```serv
let label = format.plural(3, "item", "items") // "3 items"
```

## IP Address Namespace (`ip`)

Provides IP address parsing and classifications:

- **`ip.parse(ipStr string) map`**: Parses IP address to `{ version, octets }` map.
- **`ip.isPrivate(ipStr string) bool`**: Checks if IP falls inside private subnets.
- **`ip.inCIDR(ipStr string, cidr string) bool`**: Checks if IP is within specified subnet range.
- **`ip.version(ipStr string) string`**: Returns `"ipv4"`, `"ipv6"`, or `""`.

```serv
let private = ip.isPrivate("192.168.1.1") // true
```

## DNS Resolver Namespace (`dns`)

Exposes basic network lookup functions:

- **`dns.lookup(host string) string`**: Resolves domain to its first IP string.
- **`dns.txt(host string) string`**: Returns resolved TXT strings joined by spaces.
- **`dns.srv(service string) map`**: Resolves SRV record to `{ host, port, priority }` map.

```serv
let ip = dns.lookup("example.com")
```

## Multipart Form Parsing (`multipart`)

Parses multipart form payload from HTTP request body:

- **`multipart.parse(req Request) map`**: Parses request, returning `{ fields: {...}, files: [{ name, filename, size, content }] }`.

```serv
let form = multipart.parse(req)
let username = form.fields.username
let avatarFile = form.files[0]
```

