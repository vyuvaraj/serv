package compiler

import (
	"strings"
	"testing"
)

func parseAndAnalyze(t *testing.T, input string) []Diagnostic {
	t.Helper()
	l := NewLexer(input)
	p := NewParser(l)
	program := p.ParseProgram()
	if len(p.Errors()) > 0 {
		t.Fatalf("Parser error: %v", p.Errors())
	}
	return Analyze(program)
}

func TestAnalyzeUnusedVars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string // substrings expected in diagnostics messages
	}{
		{
			name: "unused variable in function",
			input: `
				fn calculate() {
					let x = 10
				}
			`,
			expected: []string{"variable 'x' is declared but never used"},
		},
		{
			name: "used variable in function",
			input: `
				fn calculate() {
					let x = 10
					return x
				}
			`,
			expected: nil,
		},
		{
			name: "unused block variable",
			input: `
				route "GET" "/test" (req) {
					let y = 20
				}
			`,
			expected: []string{"variable 'y' is declared but never used"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := parseAndAnalyze(t, tt.input)
			var foundMsgs []string
			for _, d := range diags {
				foundMsgs = append(foundMsgs, d.Message)
			}

			if len(tt.expected) == 0 && len(diags) > 0 {
				t.Errorf("Expected no warnings, got: %v", foundMsgs)
			}

			for _, exp := range tt.expected {
				matched := false
				for _, msg := range foundMsgs {
					if contains(msg, exp) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("Expected warning message containing %q, got: %v", exp, foundMsgs)
				}
			}
		})
	}
}

func TestAnalyzeMissingReturns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "fn with missing return",
			input: `
				fn getValue() -> int {
					let x = 10
				}
			`,
			expected: []string{"function 'getValue' declares return type 'int' but may not return a value on all paths"},
		},
		{
			name: "fn with return on all paths",
			input: `
				fn getValue(cond) -> int {
					if (cond) {
						return 1
					} else {
						return 0
					}
				}
			`,
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := parseAndAnalyze(t, tt.input)
			var foundMsgs []string
			for _, d := range diags {
				foundMsgs = append(foundMsgs, d.Message)
			}

			if len(tt.expected) == 0 {
				for _, msg := range foundMsgs {
					if contains(msg, "declares return type") {
						t.Errorf("Unexpected missing return warning: %s", msg)
					}
				}
			}

			for _, exp := range tt.expected {
				matched := false
				for _, msg := range foundMsgs {
					if contains(msg, exp) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("Expected warning message containing %q, got: %v", exp, foundMsgs)
				}
			}
		})
	}
}

func TestAnalyzeUnreachableCode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "code after return",
			input: `
				fn doWork() {
					return 1
					let x = 2
				}
			`,
			expected: []string{"unreachable code after return/break/continue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := parseAndAnalyze(t, tt.input)
			var foundMsgs []string
			for _, d := range diags {
				foundMsgs = append(foundMsgs, d.Message)
			}

			for _, exp := range tt.expected {
				matched := false
				for _, msg := range foundMsgs {
					if contains(msg, exp) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("Expected warning containing %q, got: %v", exp, foundMsgs)
				}
			}
		})
	}
}

func TestAnalyzeSQLInjection(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "concatenation SQL injection query",
			input: `
				fn unsafeQuery(id) {
					db.query("SELECT * FROM users WHERE id = " + id)
				}
			`,
			expected: []string{"SQL injection risk detected"},
		},
		{
			name: "interpolation SQL injection query",
			input: `
				fn unsafeQuery2(name) {
					db.query(f"SELECT * FROM users WHERE name = '{name}'")
				}
			`,
			expected: []string{"SQL injection risk detected"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := parseAndAnalyze(t, tt.input)
			var foundMsgs []string
			for _, d := range diags {
				foundMsgs = append(foundMsgs, d.Message)
			}

			for _, exp := range tt.expected {
				matched := false
				for _, msg := range foundMsgs {
					if contains(msg, exp) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("Expected warning containing %q, got: %v", exp, foundMsgs)
				}
			}
		})
	}
}

func TestAnalyzeDomainBoundariesViolation(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "direct internal call from non-auth domain",
			input: `
				route "GET" "/user-info" (req) {
					auth_private_check_session()
				}
			`,
			expected: []string{"Domain boundary violation"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := parseAndAnalyze(t, tt.input)
			var foundMsgs []string
			for _, d := range diags {
				foundMsgs = append(foundMsgs, d.Message)
			}

			for _, exp := range tt.expected {
				matched := false
				for _, msg := range foundMsgs {
					if contains(msg, exp) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("Expected warning containing %q, got: %v", exp, foundMsgs)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCrossServiceContracts(t *testing.T) {
	// 1. Mismatched payload type
	inputPayloadMismatch := `
	declare module "service-b" {
		route POST "/orders" (req: string) -> string
	}
	fn clientCall() {
		http.post("serv://service-b/orders", 42) // expects string, got int
	}
	`
	diags := parseAndAnalyze(t, inputPayloadMismatch)
	found := false
	for _, d := range diags {
		if strings.Contains(d.Message, "request payload type mismatch in cross-service call") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected payload type mismatch error, got diagnostics: %v", diags)
	}

	// 2. Undeclared service name
	inputUndeclaredService := `
	fn clientCall() {
		http.get("serv://service-c/users")
	}
	`
	diags = parseAndAnalyze(t, inputUndeclaredService)
	found = false
	for _, d := range diags {
		if strings.Contains(d.Message, "use of undeclared service 'service-c'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected undeclared service error, got: %v", diags)
	}

	// 3. Undeclared route path
	inputUndeclaredRoute := `
	declare module "service-b" {
		route GET "/users" (req) -> string
	}
	fn clientCall() {
		http.get("serv://service-b/orders") // does not exist in module
	}
	`
	diags = parseAndAnalyze(t, inputUndeclaredRoute)
	found = false
	for _, d := range diags {
		if strings.Contains(d.Message, "service 'service-b' does not declare route 'GET /orders'") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected undeclared route error, got: %v", diags)
	}

	// 4. Correct call
	inputCorrect := `
	declare module "service-b" {
		route POST "/orders" (req: string) -> string
	}
	fn clientCall() {
		http.post("serv://service-b/orders", "ok")
	}
	`
	diags = parseAndAnalyze(t, inputCorrect)
	for _, d := range diags {
		if d.Severity == "error" {
			t.Errorf("unexpected error diagnostic: %s", d.Message)
		}
	}
}
