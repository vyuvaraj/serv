# Serv Compiler Error Code Registry

This document lists all diagnostic and syntax error codes emitted by the Serv compiler, alongside examples, causes, and solutions.

---

## SRV-E001: Token Expectation Mismatch
* **Severity:** Error
* **Cause:** The parser expected a specific symbol or keyword next but encountered something else.
* **Example:**
  ```serv
  let x 42  // Error: Expected '='
  ```
* **Solution:** Fix the syntax to include the expected token. Variable declarations must use `let name = value;`.

## SRV-E002: Unrecognized Expression Syntax
* **Severity:** Error
* **Cause:** The parser encountered a token at the start of an expression that it cannot parse (e.g., misspelled keyword or stray character).
* **Example:**
  ```serv
  sett x = 42  // Error: Unrecognized prefix token
  ```
* **Solution:** Verify the keyword spelling against the list of reserved words (e.g., `let`, `fn`, `struct`).

## SRV-E003: Literal Value Parsing Error
* **Severity:** Error
* **Cause:** A literal string, integer, or floating-point value is malformed.
* **Example:**
  ```serv
  let x = 99999999999999999999999999999999  // Error: Integer overflow
  ```
* **Solution:** Ensure the format of the literal matches expected bounds and type specifications.

## SRV-E004: Unused Variable
* **Severity:** Warning
* **Cause:** A local variable was declared using `let` but never read or referenced afterward.
* **Example:**
  ```serv
  let temp = compute()  // Warning: Unused variable
  ```
* **Solution:** Remove the variable declaration, or prefix it with `_` to suppress the warning if it is intended.

## SRV-E005: Unreachable Code
* **Severity:** Warning
* **Cause:** Statements are defined after a terminal keyword (like `return` or `break`) in the same block.
* **Example:**
  ```serv
  return 42
  log.info("Finished")  // Warning: Unreachable code
  ```
* **Solution:** Remove the unreachable statements or reorganize the control flow.

## SRV-E006: Type Mismatch
* **Severity:** Error
* **Cause:** An assignment or function call argument does not match the expected type.
* **Example:**
  ```serv
  let name: string = 123  // Error: expects type 'string', but got 'integer'
  ```
* **Solution:** Ensure variable types, return types, and argument types align.

## SRV-E007: Duplicate Symbol Declaration
* **Severity:** Error
* **Cause:** A variable, function, or struct is declared multiple times in the same scope.
* **Example:**
  ```serv
  let x = 10
  let x = 20  // Error: Duplicate declaration
  ```
* **Solution:** Rename the duplicate identifier or reuse the existing declaration.

## SRV-E008: Undefined Symbol Reference
* **Severity:** Error
* **Cause:** A variable, function, or imported module is referenced but was never declared.
* **Example:**
  ```serv
  log.info(userProfile)  // Error: Undefined symbol
  ```
* **Solution:** Ensure the identifier is spelled correctly, declared in scope, or imported.

## SRV-E009: Cyclic Workflow Dependency
* **Severity:** Error
* **Cause:** Steps in a `workflow` block form a cyclic dependency graph (DAG contains cycles).
* **Example:**
  ```serv
  workflow Order(id) {
      let a = await step1(b)
      let b = await step2(a)  // Error: cyclic step dependency
  }
  ```
* **Solution:** Reorder workflow steps so that input variables flow in a directed, acyclic fashion (straight line).

## SRV-E010: Publish Payload Schema Mismatch
* **Severity:** Error
* **Cause:** The payload struct sent via `publish` does not match the declared schema under the `schemas/` directory.
* **Example:**
  ```serv
  publish "user-created" UserBad{name: "Alice"}  // Error: missing required property 'id'
  ```
* **Solution:** Update the struct definition or publication arguments to match all fields in the JSON Schema.
