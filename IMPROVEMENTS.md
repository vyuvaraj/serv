# Serv-lang: Critical Analysis & Improvement Plan

## What Works Well

- **Coherent language design** — declaring routes, schedulers, and pub/sub is genuinely simpler than raw Go boilerplate
- **Clean compiler pipeline** — single-package with clear separation (lexer → parser → AST → analysis → codegen)
- **Escape analysis for concurrent maps** — smart decision given routes run per-request
- **Full-lifecycle tooling** — build, run, watch, test, lint, fmt, REPL, dockerize, package management
- **49 stdlib modules** covering real service concerns (retry, circuit breaker, pagination, JWT, CORS, rate limiting)
- **Good documentation** — getting-started, language reference, stdlib, CLI, builtins, deployment guides

---

## Critical Issues

### 1. The Type System is Decorative, Not Structural

Types exist in syntax (`let x: int = 5`) and codegen tracks them in `map[string]string`, but there's no actual type environment, no inference engine, and no unification. The type checker only catches literal-level mismatches at call sites. It cannot reason about:

- Types flowing through assignments
- Return type correctness beyond "does the last statement have `return`?"
- Generic constraint satisfaction at compile time
- Interface implementation checking

Everything becomes `interface{}` in Go — runtime panics where a type system should catch errors at compile time.

**Recommendation:** Either commit to gradual typing (proper symbol table + type unification) or explicitly market Serv as dynamically typed and invest in better runtime error messages. The current half-measure gives false safety.

---

### 2. `go 1.26.3` in go.mod Doesn't Exist

Go is currently at 1.23.x. This works today because Go is lenient about forward version declarations, but it's a ticking time bomb on strict toolchains.

**Fix:** Change to the actual Go version used during development (e.g., `go 1.23`).

---

### 3. Error Propagation (`?` operator) is Incomplete

The codegen for `let x = expr?` generates `return nil` on failure. It doesn't propagate the actual error value. The caller gets `nil` with no diagnostic information. Compare to Rust's `?` which propagates the `Err` variant — Serv's version loses the error entirely.

**Fix:** Generate code that captures and returns the error, or at minimum logs it.

---

### 4. REPL Compiles a Full Go Binary Per Expression

Each keystroke incurs `go build` → execute → capture output. Cold iteration is 2-5+ seconds. 

**Recommendation:** Add an AST interpreter mode (tree-walking) for sub-second interactive evaluation.

---

### 5. Variable Scoping Leaks in Codegen

`genBlockStatement` copies `declaredVars` into a new map but doesn't pop correctly in all paths. Variables can leak between scopes, causing incorrect `=` (reassignment) vs `:=` (new declaration) in generated Go.

---

### 6. Watch Mode Uses Polling

500ms `filepath.Walk` polling is both wasteful and laggy. Go has `fsnotify` available and it's trivial to integrate.

---

### 7. CI is Undercooked

Only 3 test files run in CI. With 42+ examples and 49 stdlib modules, there's no assurance that changes don't break things. There are zero Go-level unit tests for the compiler package.

---

### 8. Package Registry Assumes Non-Existent Infrastructure

`serv install` and `serv publish` hit `https://registry.serv-lang.org` which almost certainly 404s. Without a fallback to Git-based dependencies, this feature is a dead end.

---

### 9. Generated Code is Noisy

Every `let` generates `var x interface{} = ...\n_ = x\n`. A liveness analysis pass could omit unnecessary blank-identifier lines.

---

### 10. No Source-Level Debugging

Generated Go has `// .srv line N` comments but no DWARF mapping or source maps. When services panic, users get Go stack traces pointing to generated code they can't read.

---

## Improvement Plan

| Priority | Improvement | Effort | Impact |
|----------|-------------|--------|--------|
| **High** | Build a real symbol table with type environments | Large | Catches bugs at compile time |
| **High** | Fix `go 1.26.3` → actual Go version | Trivial | Prevents toolchain breakage |
| **High** | Run all examples + stdlib as CI test targets | Small | Prevents regressions |
| **High** | Add Go-level unit tests for lexer/parser/codegen | Medium | Confidence in compiler correctness |
| **Medium** | Replace REPL with AST interpreter | Medium | Sub-second interactive eval |
| **Medium** | Use `fsnotify` for watch mode | Small | Better DX, less CPU waste |
| **Medium** | Fix `?` operator to propagate error values | Small | Correct error handling semantics |
| **Medium** | Fix variable scope leaks in codegen | Small | Correct Go output |
| **Low** | Source maps for debugger integration | Large | Production debugging |
| **Low** | Build a real package registry (or Git URLs) | Medium | Ecosystem growth |
| **Low** | LSP completion/hover using symbol table | Medium | Editor integration |
| **Low** | Remove `_ = x` noise from generated code | Small | Cleaner output |

---

## Architecture Notes

### What to Preserve
- Single-package compiler — easy to navigate, all in one `compiler/` directory
- Runtime as a separate importable Go package — clean boundary
- Hash-based build directories — prevents concurrent build conflicts
- init()-based infrastructure wiring — idiomatic Go pattern
- Static analysis before codegen with warnings-don't-block-build policy

### What to Refactor
- `Codegen.varTypes` / `Codegen.declaredVars` → proper scoped symbol table
- Manual type tracking → inference engine with unification
- Polling file watcher → event-driven with fsnotify
- REPL full-compile loop → interpreter for expressions, compile only for service statements

---

## Missing Features Worth Adding

1. **`async/await` with proper goroutine management** — currently `spawn` is fire-and-forget with no structured concurrency
2. **Dependency lockfile** — `serv.lock` for reproducible builds
3. **Multi-file projects** — currently everything is a single `.srv` file or flat imports
4. **Compiler error recovery** — parser stops at first error; could continue and report multiple
5. **Dead code elimination** — generated Go includes unreachable paths from unused branches
6. **Hot module replacement** — watch mode rebuilds everything; incremental compilation would be faster
