package compiler

import (
	"go/format"
	"strings"
	"testing"
)

func TestPolicyEngineParsingAndCodegen(t *testing.T) {
	input := `
policy rate_limit_policy (ctx) {
	let path = ctx["path"]
	if path == "/api/admin" {
		return false
	}
	return true
}
`

	lexer := NewLexer(input)
	parser := NewParser(lexer)
	program := parser.ParseProgram()

	if len(parser.Errors()) > 0 {
		t.Fatalf("parser errors: %v", parser.Errors())
	}

	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}

	stmt, ok := program.Statements[0].(*PolicyStmt)
	if !ok {
		t.Fatalf("expected *PolicyStmt, got %T", program.Statements[0])
	}
	if stmt.Name != "rate_limit_policy" {
		t.Errorf("expected policy name 'rate_limit_policy', got %q", stmt.Name)
	}
	if stmt.Param != "ctx" {
		t.Errorf("expected param 'ctx', got %q", stmt.Param)
	}

	codegen := NewCodegen(program)
	goCode, err := codegen.Generate()
	if err != nil {
		t.Fatalf("codegen failed: %v", err)
	}

	// Verify policy function signature
	if !strings.Contains(goCode, "func rate_limit_policy(ctx map[string]interface{}) bool") {
		t.Errorf("expected generated policy function signature, got:\n%s", goCode)
	}

	// Verify gofmt compliance: passing generated Go through format.Source should yield identical output
	reformatted, err := format.Source([]byte(goCode))
	if err != nil {
		t.Fatalf("generated code is not valid Go syntax: %v\nCode:\n%s", err, goCode)
	}

	if string(reformatted) != goCode {
		t.Errorf("generated Go code is not strictly gofmt compliant")
	}
}
