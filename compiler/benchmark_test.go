package compiler

import (
	"testing"
)

const benchSrvSource = `
struct User {
	name:   string
	email:  string?
	age:    int
	active: bool
}

fn findUser(id: int) -> User? {
	if id == 0 { return nil }
	return User { name: "Alice", email: nil, age: 30, active: true }
}

fn divide(a: int, b: int) -> int | error {
	if b == 0 { return "division by zero" }
	return a / b
}

fn greet(name: string) -> string {
	return f"Hello, {name}!"
}

fn processUsers(users: []User) {
	for user in users {
		log.info(greet(user.name))
	}
}
`

func BenchmarkLexer(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := NewLexer(benchSrvSource)
		for {
			tok := l.NextToken()
			if tok.Type == TOKEN_EOF {
				break
			}
		}
	}
}

func BenchmarkParser(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := NewLexer(benchSrvSource)
		p := NewParser(l)
		_ = p.ParseProgram()
	}
}

func BenchmarkCodegen(b *testing.B) {
	// Parse once, then benchmark codegen repeatedly
	l := NewLexer(benchSrvSource)
	p := NewParser(l)
	prog := p.ParseProgram()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cg := NewCodegen(prog)
		_, _ = cg.GenerateStatements(prog.Statements)
	}
}

func BenchmarkAnalyze(b *testing.B) {
	l := NewLexer(benchSrvSource)
	p := NewParser(l)
	prog := p.ParseProgram()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Analyze(prog)
	}
}

func BenchmarkEndToEnd(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l := NewLexer(benchSrvSource)
		p := NewParser(l)
		prog := p.ParseProgram()
		cg := NewCodegen(prog)
		_, _ = cg.GenerateStatements(prog.Statements)
		_ = Analyze(prog)
	}
}
