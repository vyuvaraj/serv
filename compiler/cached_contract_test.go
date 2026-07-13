package compiler

import (
	"strings"
	"testing"
)

func TestCachedFnParsingAndCodegen(t *testing.T) {
	input := `
cached fn calculateTotal(x: int, y: int) -> int {
	return x + y
}
`
	l := NewLexer(input)
	p := NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("unexpected parser errors: %v", p.Errors())
	}

	if len(prog.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(prog.Statements))
	}

	fnDecl, ok := prog.Statements[0].(*FnDecl)
	if !ok {
		t.Fatalf("expected *FnDecl, got %T", prog.Statements[0])
	}

	if !fnDecl.IsCached {
		t.Error("expected function to be parsed as cached")
	}

	cg := NewCodegen(prog)
	code, err := cg.Generate()
	if err != nil {
		t.Fatalf("unexpected codegen error: %v", err)
	}

	// Code should check the cache using format key
	if !strings.Contains(code, `runtime.CacheGet`) {
		t.Error("expected codegen to emit CacheGet check")
	}
	if !strings.Contains(code, `runtime.CacheSet`) {
		t.Error("expected codegen to emit CacheSet store")
	}
	if !strings.Contains(code, `fmt.Sprintf("fn:calculateTotal:%v:%v", x, y)`) {
		t.Error("expected codegen to generate correct cache key format")
	}
}

func TestRouteContractValidation(t *testing.T) {
	// 1. Correct return type matching
	inputCorrect := `
struct User {
	name: string
}

route "GET" "/user" (req) -> User {
	let u = User{name: "Alice"}
	return u
}
`
	l1 := NewLexer(inputCorrect)
	p1 := NewParser(l1)
	prog1 := p1.ParseProgram()
	if len(p1.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p1.Errors())
	}

	diags1 := analyzeStatement(prog1.Statements[1], prog1)
	for _, d := range diags1 {
		if d.Severity == "error" {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}

	// 2. Mismatched return type
	inputIncorrect := `
struct User {
	name: string
}

route "GET" "/user" (req) -> User {
	return 42
}
`
	l2 := NewLexer(inputIncorrect)
	p2 := NewParser(l2)
	prog2 := p2.ParseProgram()
	if len(p2.Errors()) > 0 {
		t.Fatalf("parser errors: %v", p2.Errors())
	}

	diags2 := analyzeStatement(prog2.Statements[1], prog2)
	foundErr := false
	for _, d := range diags2 {
		if d.Severity == "error" && strings.Contains(d.Message, "route declares return type 'User' but returns value of type 'int'") {
			foundErr = true
		}
	}
	if !foundErr {
		t.Error("expected type mismatch diagnostic error for incorrect route contract return")
	}
}
