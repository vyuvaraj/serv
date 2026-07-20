package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"serv/compiler"
)

// --- Diagnostics (parse errors + static analysis) ---

func (s *Server) analyzeAndPublishDiagnostics(uri, text string) {
	diagnostics := []Diagnostic{}
	symbols := []symbolInfo{}

	lexer := compiler.NewLexer(text)
	parser := compiler.NewParser(lexer)
	program := parser.ParseProgram()

	// Parse errors
	for _, errMsg := range parser.Errors() {
		line, col := extractLineCol(errMsg)
		diagnostics = append(diagnostics, Diagnostic{
			Range: Range{
				Start: Position{Line: line, Character: col},
				End:   Position{Line: line, Character: col + 10},
			},
			Severity: 1,
			Message:  errMsg,
			Source:   "serv",
		})
	}

	// Static analysis warnings/errors
	if len(parser.Errors()) == 0 {
		diags := compiler.Analyze(program)
		for _, d := range diags {
			severity := 2 // Warning
			if d.Severity == "error" {
				severity = 1
			}
			line := d.Line - 1
			if line < 0 {
				line = 0
			}
			col := d.Col - 1
			if col < 0 {
				col = 0
			}
			diagnostics = append(diagnostics, Diagnostic{
				Range: Range{
					Start: Position{Line: line, Character: col},
					End:   Position{Line: line, Character: col + 10},
				},
				Severity: severity,
				Message:  d.Message,
				Source:   "serv",
			})
		}
	}

	// DX.15: Route return linting — warn if a route body has no return statement
	for _, stmt := range program.Statements {
		route, ok := stmt.(*compiler.RouteStmt)
		if !ok {
			continue
		}
		if route.Body == nil || hasReturnInBlock(route.Body.Statements) {
			continue
		}
		diagnostics = append(diagnostics, Diagnostic{
			Range: Range{
				Start: Position{Line: route.Token.Line - 1, Character: route.Token.Col - 1},
				End:   Position{Line: route.Token.Line - 1, Character: route.Token.Col + len(route.Method) + len(route.Path) + 4},
			},
			Severity: 2, // Warning
			Message:  fmt.Sprintf("route %s %q has no return statement — will respond with empty 200", route.Method, route.Path),
			Source:   "serv",
		})
	}

	// Collect symbols
	var collectSymbols func(statements []compiler.Statement)
	collectSymbols = func(statements []compiler.Statement) {
		for _, stmt := range statements {
			if stmt == nil {
				continue
			}
			sym := extractSymbol(stmt)
			if sym.Name != "" {
				symbols = append(symbols, sym)
			}
			switch s := stmt.(type) {
			case *compiler.AppStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.FnDecl:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.RouteStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.MiddlewareDecl:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.ToolStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.EveryStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.CronStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.SubscribeStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			case *compiler.TryCatchStmt:
				if s.TryBody != nil {
					collectSymbols(s.TryBody.Statements)
				}
				if s.CatchBody != nil {
					collectSymbols(s.CatchBody.Statements)
				}
			case *compiler.IfStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
				if s.ElseBody != nil {
					collectSymbols(s.ElseBody.Statements)
				}
			case *compiler.ForStmt:
				if s.Body != nil {
					collectSymbols(s.Body.Statements)
				}
			}
		}
	}
	collectSymbols(program.Statements)

	s.mu.Lock()
	s.symbols[uri] = symbols
	s.mu.Unlock()

	sendNotification("textDocument/publishDiagnostics", map[string]interface{}{
		"uri":         uri,
		"diagnostics": diagnostics,
	})
}

func extractLineCol(errMsg string) (int, int) {
	line, col := 0, 0
	if strings.HasPrefix(errMsg, "[Line ") {
		parts := strings.SplitN(errMsg, "]", 2)
		if len(parts) > 0 {
			inner := strings.TrimPrefix(parts[0], "[Line ")
			coords := strings.Split(inner, ", Col ")
			if len(coords) == 2 {
				l, _ := strconv.Atoi(coords[0])
				c, _ := strconv.Atoi(coords[1])
				line = l - 1
				col = c - 1
			}
		}
	}
	return line, col
}

// --- Code Actions ---

func (s *Server) handleCodeAction(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Range        Range                  `json:"range"`
		Context      struct {
			Diagnostics []Diagnostic `json:"diagnostics"`
		} `json:"context"`
	}
	json.Unmarshal(msg.Params, &params)

	var actions []map[string]interface{}

	// Generate quick fixes based on diagnostics
	for _, diag := range params.Context.Diagnostics {
		// "variable 'x' is declared but never used" → offer to remove the line
		if strings.Contains(diag.Message, "is declared but never used") {
			// Extract variable name
			varName := ""
			if start := strings.Index(diag.Message, "'"); start >= 0 {
				end := strings.Index(diag.Message[start+1:], "'")
				if end >= 0 {
					varName = diag.Message[start+1 : start+1+end]
				}
			}
			if varName != "" {
				actions = append(actions, map[string]interface{}{
					"title": fmt.Sprintf("Remove unused variable '%s'", varName),
					"kind":  "quickfix",
					"edit": map[string]interface{}{
						"changes": map[string]interface{}{
							params.TextDocument.URI: []map[string]interface{}{
								{
									"range": map[string]interface{}{
										"start": map[string]interface{}{"line": diag.Range.Start.Line, "character": 0},
										"end":   map[string]interface{}{"line": diag.Range.Start.Line + 1, "character": 0},
									},
									"newText": "",
								},
							},
						},
					},
				})
			}
		}

		// "cannot assign nil to non-optional type 'X'" → offer to make it optional
		if strings.Contains(diag.Message, "cannot assign nil to non-optional type") {
			typeName := ""
			if start := strings.Index(diag.Message, "'"); start >= 0 {
				end := strings.Index(diag.Message[start+1:], "'")
				if end >= 0 {
					typeName = diag.Message[start+1 : start+1+end]
				}
			}
			if typeName != "" {
				s.mu.RLock()
				text := s.documents[params.TextDocument.URI]
				s.mu.RUnlock()

				lines := strings.Split(text, "\n")
				if diag.Range.Start.Line < len(lines) {
					line := lines[diag.Range.Start.Line]
					// Replace ": type" with ": type?"
					newLine := strings.Replace(line, ": "+typeName+" ", ": "+typeName+"? ", 1)
					if newLine != line {
						actions = append(actions, map[string]interface{}{
							"title": fmt.Sprintf("Make type optional (%s?)", typeName),
							"kind":  "quickfix",
							"edit": map[string]interface{}{
								"changes": map[string]interface{}{
									params.TextDocument.URI: []map[string]interface{}{
										{
											"range": map[string]interface{}{
												"start": map[string]interface{}{"line": diag.Range.Start.Line, "character": 0},
												"end":   map[string]interface{}{"line": diag.Range.Start.Line, "character": len(line)},
											},
											"newText": newLine,
										},
									},
								},
							},
						})
					}
				}
			}
		}
	}

	sendResponse(msg.ID, actions)
}

// hasReturnInBlock recursively checks whether a list of statements contains
// at least one return statement anywhere in the block tree. DX.15.
func hasReturnInBlock(stmts []compiler.Statement) bool {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *compiler.ReturnStmt:
			return true
		case *compiler.IfStmt:
			if s.Body != nil && hasReturnInBlock(s.Body.Statements) {
				return true
			}
			if s.ElseBody != nil && hasReturnInBlock(s.ElseBody.Statements) {
				return true
			}
		case *compiler.BlockStmt:
			if hasReturnInBlock(s.Statements) {
				return true
			}
		}
	}
	return false
}
