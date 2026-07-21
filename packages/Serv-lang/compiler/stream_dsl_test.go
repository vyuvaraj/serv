package compiler

import (
	"strings"
	"testing"
)

func TestStreamDSLTransformParsingAndCodegen(t *testing.T) {
	input := `
transform "orders.raw" (msg) {
	let clean = msg
	return clean
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

	stmt, ok := program.Statements[0].(*TransformStmt)
	if !ok {
		t.Fatalf("expected *TransformStmt, got %T", program.Statements[0])
	}
	if stmt.Param != "msg" {
		t.Errorf("expected param 'msg', got %q", stmt.Param)
	}

	codegen := NewCodegen(program)
	goCode, err := codegen.Generate()
	if err != nil {
		t.Fatalf("codegen failed: %v", err)
	}

	if !strings.Contains(goCode, `runtime.RegisterTransform`) || !strings.Contains(goCode, `"orders.raw"`) {
		t.Errorf("expected generated Go code to register transform for orders.raw, got:\n%s", goCode)
	}
}

func TestStaticConcurrencyGuardrails(t *testing.T) {
	input := `
fn process() {
	let counter = 0
	spawn fn() {
		let counter = counter + 1
	}
}
`

	lexer := NewLexer(input)
	parser := NewParser(lexer)
	program := parser.ParseProgram()

	if len(parser.Errors()) > 0 {
		t.Fatalf("parser errors: %v", parser.Errors())
	}

	diags := Analyze(program)
	foundWarning := false
	for _, diag := range diags {
		if strings.Contains(diag.Message, "concurrent mutation of outer variable 'counter'") {
			foundWarning = true
			break
		}
	}

	if !foundWarning {
		t.Errorf("expected concurrency warning for mutating outer variable 'counter' inside spawn, got diags: %v", diags)
	}
}
