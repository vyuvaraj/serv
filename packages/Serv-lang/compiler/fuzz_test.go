package compiler

import (
	"testing"
)

func FuzzLexer(f *testing.F) {
	// Add seed corpus
	f.Add("let x = 42")
	f.Add("fn calculate(a, b) { return a + b }")
	f.Add("struct User { name: string }")
	f.Add("publish \"events.new\" User{}")
	f.Add("every 1m { log.info(\"tick\") }")

	f.Fuzz(func(t *testing.T, input string) {
		l := NewLexer(input)
		for {
			tok := l.NextToken()
			if tok.Type == TOKEN_EOF || tok.Type == TOKEN_ILLEGAL {
				break
			}
		}
	})
}

func FuzzParser(f *testing.F) {
	// Add seed corpus
	f.Add("let x = 42")
	f.Add("fn calculate(a, b) { return a + b }")
	f.Add("struct User { name: string }")
	f.Add("import \"auth.srv\"")
	f.Add("every 1m { log.info(\"tick\") }")

	f.Fuzz(func(t *testing.T, input string) {
		l := NewLexer(input)
		p := NewParser(l)
		// Ensure parser doesn't crash, panic, or enter infinite loop
		_ = p.ParseProgram()
	})
}
