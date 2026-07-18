# Built-in Functions & Objects

Serv provides built-in objects for common service operations. No imports needed.

## log — Structured Logging

```serv
log.info("Server started")
log.warn("Slow query detected")
log.error("Connection failed: ", err)
log.debug("Processing item: ", id)

// Context logger (fields included in every log)
let logger = log.with("service", "auth", "version", "2.0")
logger.info("Request processed")

// Logger from map
let reqLog = log.fields({ "request_id": id, "user": name })
reqLog.error("Failed")

// Runtime level control
log.setLevel("debug")
let level = log.getLevel()
```

**Environment:** `LOG_FORMAT=json` for JSON output, `LOG_LEVEL=debug|info|warn|error`

## db — Database Operations

```serv
database "sqlite://app.db"        // SQLite
database "postgres://user:pass@host/db"  // PostgreSQL
database "mongodb://localhost:27017/mydb"  // MongoDB

// Query (SQL or MongoDB)
let rows = db.query("SELECT * FROM users WHERE active = ?", true)
let result = db.query("INSERT INTO users (name) VALUES (?)", "Alice")

// MongoDB-specific
let page = db.queryPage("users", "{}", 1, 20)
let user = db.findOne("users", "{\"email\": \"a@test.com\"}")
let count = db.count("users", "{\"active\": true}")
let res = db.upsert("users", filter, update)
```

## cache — Caching

```serv
cache "redis://localhost:6379"    // Redis
cache "in-memory"                 // Local (dev/testing)

cache.set("key", value, "60s")   // Set with TTL
let val = cache.get("key")       // Get (nil if expired/missing)
```

## http — HTTP Client

```serv
let resp = http.get("https://api.example.com/data")
// resp.status = 200, resp.body = "..."

let resp = http.post("https://api.example.com/users", body)
```

## json — JSON Operations

```serv
let obj = json.parse("{\"name\": \"Alice\"}")
let str = json.stringify({ "name": "Alice" })
```

## time — Time Operations

```serv
let now = time.now()       // ISO 8601 timestamp
let ts = time.unix()       // Unix timestamp (int)
time.sleep(1000)           // Sleep milliseconds
```

## env — Environment Variables & Secrets

```serv
let port = env("PORT")            // Read env var (empty string if not set)
let password = env.secret("DB_PASSWORD") // Retrieve secret value dynamically from KMS/vault
```

## config — Configuration

```serv
let host = config("db.host")   // Read from config.yml or env
```

Reads from `config.yml` in the working directory, or maps dotted keys to env vars (`db.host` → `DB_HOST`).

## metric — Metrics

```serv
metric.inc("requests_total")
metric.gauge("active_connections", 42)
```

Exposed at `GET /metrics` endpoint.

## publish / subscribe — Messaging

```serv
publish "topic" "message"

subscribe "topic" (msg) {
    log.info("Received: ", msg)
}
```

## atomic — Atomic Operations

```serv
atomic.new("counter", 0)
atomic.inc("counter")
atomic.dec("counter")
let val = atomic.get("counter")
atomic.set("counter", 100)
atomic.cas("counter", 100, 200)  // Compare-and-swap
```

## channel — Go Channels

```serv
let ch = channel.new("mychan", 10)  // Buffered channel
channel.send("mychan", "data")
let msg = channel.receive("mychan")
let msg = channel.tryReceive("mychan")  // Non-blocking
channel.close("mychan")
```

## registry — Named Function Registry

```serv
registry.set("handler", fn(x) { return x * 2 })
let result = registry.call("handler", 5)  // 10
registry.has("handler")  // true
registry.list()          // ["handler"]
```

## validate — Request Validation

```serv
let errors = validate(req.body, {
    "email": "required,email",
    "name": "required,string",
    "age": "int"
})
// Returns nil if valid, or ["email is required", ...] if invalid
```

**Rules:** `required`, `string`, `int`, `float`, `bool`, `email` — combine with commas.

## String Methods

```serv
"hello world".split(" ")      // ["hello", "world"]
"  hi  ".trim()               // "hi"
"hello".replace("l", "L")     // "heLLo"
"hello".startsWith("he")      // true
"hello".endsWith("lo")        // true
"hello".includes("ell")       // true
"hello".toUpper()             // "HELLO"
"HELLO".toLower()             // "hello"
"hello".substring(1, 3)       // "el"
"hello".indexOf("l")          // 2
"ha".repeat(3)                // "hahaha"
"hello".length()              // 5
```

## Collection Methods

```serv
let items = [1, 2, 3, 4, 5]

items.filter(x => x > 2)        // [3, 4, 5]
items.map(x => x * 2)           // [2, 4, 6, 8, 10]
items.find(x => x == 3)         // 3
items.reduce(fn(a, b) { return a + b }, 0)  // 15
items.forEach(x => log.info(x))
items.contains(3)                // true
items.push(6)                    // [1, 2, 3, 4, 5, 6]
items.length()                   // 5

## ai — Artificial Intelligence & LLM Access

First-class AI operations directly available in Serv-lang:

```serv
// Complete single prompt
let res = ai.complete("Translate to French: Hello World")
// res = "Bonjour le monde"

// Chat conversation loop
let reply = ai.chat([
    { "role": "system", "content": "You are a translator." },
    { "role": "user", "content": "Translate: Hello" }
])

// Vector Embeddings generation
let vectors = ai.embed("text to convert to vectors")
// vectors = [0.12, -0.45, 0.98, ...]
```

## auth — Authenticated Request Claims

Inspect and parse request JWT contexts:

```serv
let claims = auth.claims(req)
// claims = { "username": "alice", "roles": ["admin"] }

let valid = auth.verify(req)  // Returns true if authenticated
```

## exec — Shell Command Execution

Runs shell commands natively:

```serv
let result = exec.run("echo 'Hello'")
// result = { stdout: "Hello\n", stderr: "", exitCode: 0 }
```

- **`exec.run(cmdStr string) map`**: Runs `cmdStr` under `powershell` (Windows) or `sh` (Linux). Returns a map with `stdout`, `stderr`, and `exitCode`.

## file — Direct File I/O

Enables reading and writing files directly:

```serv
let ok = file.write("./log.txt", "data")
let content = file.read("./log.txt")
let exists = file.exists("./log.txt")
let list = file.list(".")
```

- **`file.read(path string) string`**: Reads file contents to string.
- **`file.write(path string, content string) bool`**: Writes content to file.
- **`file.exists(path string) bool`**: Checks if file or directory exists.
- **`file.list(path string) []string`**: Lists directory items.

## csv — CSV Parsing & Stringification

- **`csv.parse(content string) [][]string`**: Parses CSV content to a list of row arrays.
- **`csv.stringify(rows [][]string, headers []string) string`**: Stringifies matrix rows and optional headers to CSV format.

## xml — XML Parsing & Serialization

- **`xml.parse(content string) map`**: Parses XML document into a nested map structure.
- **`xml.stringify(obj map/slice) string`**: Encodes nested map/slice structure into XML string.

## yaml — YAML Parsing & Serialization

- **`yaml.parse(content string) map/slice`**: Unmarshals YAML string to generic structure.
- **`yaml.stringify(obj map/slice) string`**: Marshals generic structure to YAML string.

## path — File Path Utilities

- **`path.join(args... string) string`**: Joins path components using the OS separator.
- **`path.dirname(p string) string`**: Returns directory portion of path.
- **`path.basename(p string) string`**: Returns last element of path.
- **`path.ext(p string) string`**: Returns file extension.
- **`path.abs(p string) string`**: Returns absolute representation of path.

## regex — Regular Expressions

- **`regex.match(pattern string, value string) bool`**: Checks if pattern matches value.
- **`regex.find(pattern string, value string) string`**: Returns first match text.
- **`regex.replace(pattern string, value string, replacement string) string`**: Replaces all matches in value with replacement.

## math — Math Functions

- **`math.floor(x float) float`**: Returns greatest integer less than or equal to x.
- **`math.ceil(x float) float`**: Returns least integer greater than or equal to x.
- **`math.round(x float) float`**: Returns nearest integer.
- **`math.abs(x float) float`**: Returns absolute value of x.
- **`math.pow(base float, exp float) float`**: Returns base raised to exp.
- **`math.sqrt(x float) float`**: Returns square root of x.
- **`math.min(a float, b float) float`**: Returns minimum of two numbers.
- **`math.max(a float, b float) float`**: Returns maximum of two numbers.

## encoding — Base64 & Hex

- **`encoding.base64.encode(str string) string`**: Encodes string to base64.
- **`encoding.base64.decode(str string) string`**: Decodes base64 string.
- **`encoding.hex.encode(str string) string`**: Encodes string to hex.
- **`encoding.hex.decode(str string) string`**: Decodes hex string.

## hash — Cryptographic Digests

- **`hash.md5(str string) string`**: Generates MD5 hex hash.
- **`hash.sha256(str string) string`**: Generates SHA-256 hex hash.
- **`hash.sha512(str string) string`**: Generates SHA-512 hex hash.
- **`hash.hmac(key string, data string, algo string) string`**: Computes HMAC hex signature using specified algorithm (`"md5"`, `"sha256"`, `"sha512"`).
