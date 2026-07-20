package compiler

import (
	"testing"
)

func TestLexerStringsAndComments(t *testing.T) {
	input := `
	// This is a line comment
	// This is another line comment
	let x = "standard string"
	let y = ` + "`raw string`" + `
	let z = f"interpolated {name} string"
	`

	l := NewLexer(input)
	expected := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TOKEN_LET, "let"},
		{TOKEN_IDENT, "x"},
		{TOKEN_ASSIGN, "="},
		{TOKEN_STRING, "standard string"},
		{TOKEN_LET, "let"},
		{TOKEN_IDENT, "y"},
		{TOKEN_ASSIGN, "="},
		{TOKEN_STRING, "raw string"},
		{TOKEN_LET, "let"},
		{TOKEN_IDENT, "z"},
		{TOKEN_ASSIGN, "="},
		{TOKEN_FSTRING, "interpolated {name} string"},
	}

	for i, tt := range expected {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}

func TestLexerDurationsAndFloats(t *testing.T) {
	input := "10s 500ms 2.5 0.05"
	l := NewLexer(input)

	expected := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{TOKEN_DURATION, "10s"},
		{TOKEN_DURATION, "500ms"},
		{TOKEN_FLOAT, "2.5"},
		{TOKEN_FLOAT, "0.05"},
	}

	for i, tt := range expected {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Errorf("tests[%d] - tokentype wrong. expected=%q, got=%q", i, tt.expectedType, tok.Type)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Errorf("tests[%d] - literal wrong. expected=%q, got=%q", i, tt.expectedLiteral, tok.Literal)
		}
	}
}
