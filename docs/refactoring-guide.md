# Compiler Refactoring Guide

## Overview

Split `parser.go` (1856 lines) and `codegen.go` (2192 lines) into smaller, focused files while keeping everything in the same `compiler` Go package.

**Important:** All files stay in `compiler/` directory with `package compiler`. No sub-packages — Go allows multiple files in the same package. This means zero import changes anywhere.

---

## Target Structure

```
compiler/
├── ast.go                  (keep as-is — 854 lines, well-scoped)
├── lexer.go                (keep as-is — 466 lines, well-scoped)
├── errors.go               (keep as-is — diagnostic formatting)
│
├── parser.go               → parser_core.go
├── (new) parser_expr.go
├── (new) parser_stmt.go
├── (new) parser_services.go
├── (new) parser_functions.go
├── (new) parser_types.go
├── (new) parser_controlflow.go
│
├── codegen.go              → codegen_core.go
├── (new) codegen_expr.go
├── (new) codegen_stmt.go
├── (new) codegen_services.go
├── (new) codegen_types.go
├── (new) codegen_testing.go
├── (new) codegen_helpers.go
```

---

## Parser Split

### `parser_core.go` (keep as `parser.go`, shrink to ~220 lines)
Infrastructure: struct, constructor, token navigation, program parsing, statement dispatch.

| Function | Line | Keep here |
|----------|------|-----------|
| `NewParser` | 57 | ✅ |
| `registerPrefix` | 103 | ✅ |
| `registerInfix` | 107 | ✅ |
| `nextToken` | 111 | ✅ |
| `Errors` | 116 | ✅ |
| `addError` | 120 | ✅ |
| `ParseProgram` | 124 | ✅ |
| `parseStatement` (dispatch switch) | 139 | ✅ |
| `expectPeek` | 1258 | ✅ |
| `peekPrecedence` | 1292 | ✅ |
| `curPrecedence` | 1304 | ✅ |
| `peekError` | 1311 | ✅ |
| `isParamListFollowedByBrace` | 1267 | ✅ |
| `parseBlockStatement` | 858 | ✅ |
| `parseExpressionStatement` | 875 | ✅ |

Also keep: precedence constants, precedence map, Parser struct, type definitions.

### `parser_expr.go` (~450 lines)
All expression parsing functions.

| Function | Current Line |
|----------|-------------|
| `parseExpression` | 881 |
| `parseIdentifier` | 902 |
| `isGenericCallAhead` | 942 |
| `isStructLiteralAhead` | 975 |
| `parseStructLiteral` | 996 |
| `parseIntegerLiteral` | 1033 |
| `parseStringLiteral` | 1046 |
| `parseFloatLiteral` | 1050 |
| `parseDurationLiteral` | 1061 |
| `parseArrayLiteral` | 1065 |
| `parseMapLiteral` | 1084 |
| `parseGroupedExpression` | 1134 |
| `parseInfixExpression` | 1144 |
| `parseCallExpression` | 1158 |
| `parseExpressionList` | 1164 |
| `parseMemberExpression` | 1218 |
| `parseOptionalMemberExpression` | 1229 |
| `parseIndexExpression` | 1315 |
| `parseAssignmentExpression` | 1240 |
| `parseFStringLiteral` | 850 |
| `parseCacheIdentifier` | 854 |
| `parseAssertExpression` | 1210 |
| `parseBooleanLiteral` | 1476 |
| `parseNilLiteral` | 1480 |
| `parseSelfExpression` | 1500 |
| `parseValidateIdentifier` | 1376 |

**Imports needed:** `"fmt"`, `"strconv"`

### `parser_stmt.go` (~300 lines)
General statement parsing.

| Function | Current Line |
|----------|-------------|
| `parseImportStatement` | 214 |
| `parseExternStatement` | 264 |
| `parseLetStatement` | 539 |
| `parseReturnStatement` | 601 |
| `parseTestStatement` | 1189 |
| `parseExportStatement` | 1768 |
| `parseDatabaseStatement` | 793 |
| `parseCacheStatement` | 800 |
| `parseBrokerStatement` | 308 |
| `parsePublishStatement` | 513 |
| `parseSpawnStatement` | 524 |

**Imports needed:** (none beyond what parser_core provides)

### `parser_services.go` (~250 lines)
Service-infrastructure parsing: routes, middleware, websockets, schedulers.

| Function | Current Line |
|----------|-------------|
| `parseServerStatement` | 315 |
| `parseRouteStatement` | 337 |
| `parseToolStatement` | 413 |
| `parseMigrationStatement` | 447 |
| `parseEveryStatement` | 463 |
| `parseCronStatement` | 475 |
| `parseSubscribeStatement` | 487 |
| `parseMiddlewareDeclaration` | 1737 |
| `parseWsStatement` | 1620 |
| `parseDeclareStatement` | 1648 |

**Imports needed:** (none)

### `parser_functions.go` (~200 lines)
Function/method/closure parsing.

| Function | Current Line |
|----------|-------------|
| `parseFnDeclaration` | 608 |
| `parseMethodDeclaration` | 708 |
| `parseFnLiteral` | 1512 |
| `parseArrowFunction` | 1558 |

**Imports needed:** (none)

### `parser_types.go` (~180 lines)
Type system parsing: structs, interfaces, enums, type aliases, annotations.

| Function | Current Line |
|----------|-------------|
| `parseStructDeclaration` | 1577 |
| `parseInterfaceDeclaration` | 1781 |
| `parseEnumStatement` | 1325 |
| `parseTypeAliasStatement` | 1359 |
| `parseValidateStatement` | 1381 |
| `parseTypeAnnotation` | 1488 |

**Imports needed:** (none)

### `parser_controlflow.go` (~180 lines)
Control flow parsing.

| Function | Current Line |
|----------|-------------|
| `parseIfStatement` | 1413 |
| `parseForStatement` | 1450 |
| `parseMatchStatement` | 807 |
| `parseTryCatchStatement` | 760 |
| `parseAwaitExpression` | 1504 |

**Imports needed:** (none)

---

## Codegen Split

### `codegen_core.go` (keep as `codegen.go`, shrink to ~250 lines)
Struct, constructor, Generate(), genStatement dispatch, genBlockStatement.

| Function | Current Line | Keep here |
|----------|-------------|-----------|
| `NewCodegen` | 27 | ✅ |
| `Generate` | 43 | ✅ |
| `genStatement` (dispatch only) | 161 | ✅ |
| `genBlockStatement` | 871 | ✅ |
| `GenerateMainFunc` | 1668 | ✅ |

Also keep: Codegen struct definition, all field declarations.

### `codegen_expr.go` (~700 lines)
The `genExpression` function and its helpers.

| Function | Current Line |
|----------|-------------|
| `genExpression` (entire switch) | 903–1660 |
| `genCollectionCallback` | 1822 |
| `genReduceCallback` | 1860 |
| `genFString` | 1871 |
| `compileInlineExpr` | 1925 |

**Imports needed:** `"fmt"`, `"strings"`, `"path/filepath"`

### `codegen_stmt.go` (~600 lines)
Statement generation (extract from `genStatement` body).

Extract these `case` branches from `genStatement`:
- `*ImportStmt`, `*GoPackageImport`, `*DeclareModuleStmt`, `*ExportStmt`
- `*ExternFnStmt`, `*EnumStmt`, `*TypeAliasStmt`, `*ValidateStmt`
- `*IfStmt`, `*ForStmt`, `*MatchStmt`, `*TryCatchStmt`
- `*LetStmt`, `*DestructureLetStmt`, `*ReturnStmt`, `*ExprStmt`
- `*FnDecl`, `*MethodDecl`, `*TestStmt`
- `*SpawnStmt`, `*PublishStmt`

**Approach:** Create helper methods like `genLetStmt(s *LetStmt)`, `genFnDecl(s *FnDecl)`, etc. The dispatch in `genStatement` calls them. This way `codegen_core.go` has the dispatch and `codegen_stmt.go` has the implementations.

### `codegen_services.go` (~200 lines)
Service infrastructure generation.

Extract from `genStatement`:
- `*BrokerStmt`, `*ServerStmt`, `*DatabaseStmt`, `*CacheStmt`
- `*RouteStmt`, `*MiddlewareDecl`, `*WsStmt`
- `*EveryStmt`, `*CronStmt`, `*SubscribeStmt`
- `*ToolStmt`, `*MigrationStmt`

### `codegen_types.go` (~200 lines)
Type system utilities.

| Function | Current Line |
|----------|-------------|
| `toGoType` | 1936 |
| `servConstraintToGo` | 1961 |
| `zeroValue` | 1985 |
| `getExpressionType` | 2000 |
| `capitalizeFirst` | 1792 |
| `sanitizeTestName` | 1801 |

Also extract from `genStatement`:
- `*StructDecl`, `*InterfaceDecl`

### `codegen_testing.go` (~80 lines)
Test generation.

| Function | Current Line |
|----------|-------------|
| `HasTests` | 1750 |
| `GenerateTests` | 1757 |

### `codegen_helpers.go` (~100 lines)
Helper function generation and utilities.

| Function | Current Line |
|----------|-------------|
| `GenerateHelpers` | 1676 |
| `hasConcurrency` | 2075 |
| `stmtToken` | 2118 |

---

## Execution Steps

### Phase 1: Parser Split (safest first)

```bash
# 1. Create new files with correct package header
# 2. Move functions (cut from parser.go, paste into new file)
# 3. Add required imports to each new file
# 4. Build to verify: go build ./compiler
# 5. Run regression: powershell -ExecutionPolicy Bypass -File test_regression.ps1 -Phase 1
```

**Order of operations:**
1. Create `parser_expr.go` — move all expression functions
2. Build & test
3. Create `parser_controlflow.go` — move if/for/match/try
4. Build & test
5. Create `parser_types.go` — move struct/interface/enum
6. Build & test
7. Create `parser_services.go` — move route/ws/middleware/scheduler
8. Build & test
9. Create `parser_functions.go` — move fn/method/arrow
10. Build & test

### Phase 2: Codegen Split

```bash
# Same approach — one file at a time, build after each
```

**Order of operations:**
1. Create `codegen_types.go` — move type helpers (smallest, safest)
2. Build & test
3. Create `codegen_helpers.go` — move GenerateHelpers, hasConcurrency, stmtToken
4. Build & test
5. Create `codegen_testing.go` — move HasTests, GenerateTests
6. Build & test
7. Create `codegen_expr.go` — move genExpression (largest piece)
8. Build & test
9. Create `codegen_services.go` — extract service cases from genStatement
10. Build & test

---

## Important Notes

1. **Same package, same directory** — all files must be `package compiler` and live in `compiler/`. Go compiles all `.go` files in a directory as one package.

2. **No circular dependencies** — since it's one package, all types and functions are accessible across files.

3. **Import deduplication** — each file only needs imports for what it directly uses. The `fmt`, `strings`, `strconv` imports move to wherever they're actually called.

4. **Don't rename functions** — just move them. No external API changes.

5. **Build after every move** — `go build ./compiler` catches any issues immediately.

6. **One function group at a time** — never move more than one category before verifying the build.

---

## Future: Semantic Analysis Layer

After the split is complete, add a new phase between parsing and codegen:

```
compiler/
├── ...existing files...
├── analyze.go            — Semantic analysis (new)
│   • Variable scope validation
│   • Undefined variable detection  
│   • Type inference propagation
│   • "Did you mean?" for identifiers
│   • Dead code detection
└── typecheck.go          — Type checking (new)
    • Assignment compatibility
    • Function argument type checking
    • Return type verification
    • Generic constraint validation
```

The pipeline becomes:
```
Lexer → Parser → AST → Analyze → TypeCheck → Codegen → Go Source
```

This is a separate effort from the file split and should be its own branch.
