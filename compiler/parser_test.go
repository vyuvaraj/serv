package compiler

import (
	"testing"
)

func TestParserOptionalTypes(t *testing.T) {
	input := `
	fn findUser(id: int) -> User? {
		return nil
	}
	`
	l := NewLexer(input)
	p := NewParser(l)
	program := p.ParseProgram()

	if len(p.Errors()) > 0 {
		t.Fatalf("Parser had errors: %v", p.Errors())
	}

	if len(program.Statements) != 1 {
		t.Fatalf("Expected 1 statement, got %d", len(program.Statements))
	}

	fn, ok := program.Statements[0].(*FnDecl)
	if !ok {
		t.Fatalf("Expected FnDecl statement, got %T", program.Statements[0])
	}

	if fn.ReturnType != "User?" {
		t.Errorf("Expected return type User?, got %q", fn.ReturnType)
	}
}

func TestParserRoutesAndTryCatch(t *testing.T) {
	input := `
	route "POST" "/orders" (req) {
		try {
			let order = req.body
			return { "status": "ok" }
		} catch (e) {
			return { "status": "error", "message": e }
		}
	}
	`
	l := NewLexer(input)
	p := NewParser(l)
	program := p.ParseProgram()

	if len(p.Errors()) > 0 {
		t.Fatalf("Parser had errors: %v", p.Errors())
	}

	if len(program.Statements) != 1 {
		t.Fatalf("Expected 1 statement, got %d", len(program.Statements))
	}

	route, ok := program.Statements[0].(*RouteStmt)
	if !ok {
		t.Fatalf("Expected RouteStmt statement, got %T", program.Statements[0])
	}

	if route.Method != "POST" || route.Path != "/orders" {
		t.Errorf("Expected route POST /orders, got %s %s", route.Method, route.Path)
	}
}
