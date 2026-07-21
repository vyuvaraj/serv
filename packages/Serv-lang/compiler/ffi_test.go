package compiler

import (
	"strings"
	"testing"
)

func TestGoFFIExternBindingCodegen(t *testing.T) {
	input := `
extern fn generate_uuid() from "go:github.com/google/uuid:NewString"

route "GET" "/uuid" (req) {
	let id = generate_uuid()
	return { "uuid": id }
}
`

	lexer := NewLexer(input)
	parser := NewParser(lexer)
	program := parser.ParseProgram()

	if len(parser.Errors()) > 0 {
		t.Fatalf("parser errors: %v", parser.Errors())
	}

	codegen := NewCodegen(program)
	goCode, err := codegen.Generate()
	if err != nil {
		t.Fatalf("codegen failed: %v", err)
	}

	// Verify that the external Go package is imported
	if !strings.Contains(goCode, `"github.com/google/uuid"`) {
		t.Errorf("expected generated Go code to contain import github.com/google/uuid, got:\n%s", goCode)
	}

	// Verify that the extern function call is invoked correctly
	if !strings.Contains(goCode, "uuid.NewString()") {
		t.Errorf("expected generated Go code to invoke uuid.NewString(), got:\n%s", goCode)
	}
}

func TestGoFFIMethodReceiverCodegen(t *testing.T) {
	input := `
extern fn decimal_to_string(dec) from "go:github.com/shopspring/decimal:Decimal.String"

route "GET" "/decimal" (req) {
	let val = req.query["val"]
	let str = decimal_to_string(val)
	return { "formatted": str }
}
`

	lexer := NewLexer(input)
	parser := NewParser(lexer)
	program := parser.ParseProgram()

	if len(parser.Errors()) > 0 {
		t.Fatalf("parser errors: %v", parser.Errors())
	}

	codegen := NewCodegen(program)
	goCode, err := codegen.Generate()
	if err != nil {
		t.Fatalf("codegen failed: %v", err)
	}

	// Verify package import
	if !strings.Contains(goCode, `"github.com/shopspring/decimal"`) {
		t.Errorf("expected generated Go code to contain import github.com/shopspring/decimal, got:\n%s", goCode)
	}
}
