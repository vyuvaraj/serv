package compiler

import (
	"fmt"
	"strings"
)

// Diagnostic represents a compiler warning or error from static analysis.
type Diagnostic struct {
	Line     int
	Col      int
	Severity string // "warning" or "error"
	Message  string
}

// Analyze performs static analysis on a parsed program and returns diagnostics.
// This includes:
// - Unused variable detection
// - Missing return detection for typed functions
// - Basic type mismatch checking for function calls
// - Unreachable code detection
// - Dead import detection
func Analyze(program *Program) []Diagnostic {
	var diags []Diagnostic

	// Collect all function/struct/let names defined at top level (for dead import detection)
	definedSymbols := make(map[string]bool)
	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *FnDecl:
			definedSymbols[s.Name] = true
		case *StructDecl:
			definedSymbols[s.Name] = true
		case *LetStmt:
			definedSymbols[s.Name] = true
		case *EnumStmt:
			definedSymbols[s.Name] = true
		case *ExportStmt:
			name := statementNameForAnalysis(s.Inner)
			if name != "" {
				definedSymbols[name] = true
			}
		}
	}

	// Check for dead imports
	diags = append(diags, analyzeDeadImports(program)...)

	// Check for SQL injection risks
	diags = append(diags, analyzeSQLInjection(program)...)

	// ARCH.8: Bounded context domain boundaries checks
	diags = append(diags, analyzeDomainBoundaries(program)...)

	// LANG.8: Interface satisfaction checking
	diags = append(diags, analyzeInterfaceSatisfaction(program)...)

	// LANG.3: Compiler plugin system
	diags = append(diags, LoadAndRunPlugins(program, ".")...)

	for _, stmt := range program.Statements {
		diags = append(diags, analyzeStatement(stmt, program)...)
	}

	return diags
}

// statementNameForAnalysis extracts the declared name from a statement.
func statementNameForAnalysis(stmt Statement) string {
	switch s := stmt.(type) {
	case *FnDecl:
		return s.Name
	case *StructDecl:
		return s.Name
	case *LetStmt:
		return s.Name
	case *EnumStmt:
		return s.Name
	default:
		return ""
	}
}

func analyzeStatement(stmt Statement, program *Program) []Diagnostic {
	switch s := stmt.(type) {
	case *FnDecl:
		return analyzeFnDecl(s, program)
	case *ExportStmt:
		return analyzeStatement(s.Inner, program)
	case *RouteStmt:
		return analyzeBlock(s.Body, nil)
	case *EveryStmt:
		return analyzeBlock(s.Body, nil)
	case *CronStmt:
		return analyzeBlock(s.Body, nil)
	case *SubscribeStmt:
		return analyzeBlock(s.Body, nil)
	case *MiddlewareDecl:
		return analyzeBlock(s.Body, nil)
	case *TestStmt:
		return analyzeBlock(s.Body, nil)
	case *AppStmt:
		return analyzeBlock(s.Body, nil)
	case *AgentDecl:
		return nil
	case *MeshStmt:
		return analyzeBlock(s.Body, nil)
	case *OnStmt:
		return analyzeBlock(s.Body, nil)
	case *LockStmt:
		return analyzeBlock(s.Body, nil)
	case *BucketStmt:
		return analyzeBlock(s.Body, nil)
	case *GateStmt:
		return analyzeBlock(s.Body, nil)
	case *JobStmt:
		return analyzeBlock(s.Body, nil)
	case *RagStmt:
		return analyzeBlock(s.Body, nil)
	case *EmitStmt:
		return nil
	case *CommandDecl:
		return analyzeBlock(s.Body, nil)
	case *EventStoreStmt:
		var diags []Diagnostic
		for _, c := range s.Commands {
			if d := analyzeBlock(c.Body, nil); len(d) > 0 {
				diags = append(diags, d...)
			}
		}
		for _, h := range s.Handlers {
			if d := analyzeBlock(h.Body, nil); len(d) > 0 {
				diags = append(diags, d...)
			}
		}
		return diags
	}
	return nil
}

func analyzeFnDecl(fn *FnDecl, program *Program) []Diagnostic {
	var diags []Diagnostic

	// Unused variable detection within function body
	diags = append(diags, analyzeUnusedVars(fn)...)

	// Missing return detection
	if fn.ReturnType != "" && fn.ReturnType != "nil" {
		if !blockHasReturn(fn.Body) {
			diags = append(diags, Diagnostic{
				Line:     fn.Token.Line,
				Col:      fn.Token.Col,
				Severity: "warning",
				Message:  fmt.Sprintf("function '%s' declares return type '%s' but may not return a value on all paths", fn.Name, fn.ReturnType),
			})
		}
	}

	// Unreachable code detection
	diags = append(diags, analyzeUnreachableCode(fn.Body)...)

	// Basic type checking for function calls in the body
	diags = append(diags, analyzeTypeMismatches(fn, program)...)

	return diags
}

// FormatAnalysisDiagnostics formats analysis diagnostics for display.
func FormatAnalysisDiagnostics(diags []Diagnostic, source string) string {
	if len(diags) == 0 {
		return ""
	}

	lines := strings.Split(source, "\n")
	var out strings.Builder

	for _, d := range diags {
		prefix := "warning"
		if d.Severity == "error" {
			prefix = "error"
		}
		out.WriteString(fmt.Sprintf("  %s: %s\n", prefix, d.Message))

		if d.Line > 0 && d.Line <= len(lines) {
			srcLine := lines[d.Line-1]
			lineNum := fmt.Sprintf(" %d | ", d.Line)
			out.WriteString(fmt.Sprintf("  %s%s\n", lineNum, srcLine))
			if d.Col > 0 {
				padding := strings.Repeat(" ", len(lineNum)+d.Col-1)
				out.WriteString(fmt.Sprintf("  %s^\n", padding))
			}
		}
		out.WriteString("\n")
	}

	return out.String()
}

func analyzeDomainBoundaries(program *Program) []Diagnostic {
	var diags []Diagnostic

	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *RouteStmt:
			diags = append(diags, checkBlockDomainBoundaries(s.Body, s.Path, s.Token.Line, s.Token.Col)...)
		case *EveryStmt:
			diags = append(diags, checkBlockDomainBoundaries(s.Body, "cron-every", s.Token.Line, s.Token.Col)...)
		case *CronStmt:
			diags = append(diags, checkBlockDomainBoundaries(s.Body, "cron-schedule", s.Token.Line, s.Token.Col)...)
		case *SubscribeStmt:
			diags = append(diags, checkBlockDomainBoundaries(s.Body, "subscribe-"+s.Topic.String(), s.Token.Line, s.Token.Col)...)
		}
	}

	return diags
}

func checkBlockDomainBoundaries(block *BlockStmt, contextName string, line, col int) []Diagnostic {
	var diags []Diagnostic
	if block == nil {
		return diags
	}

	for _, stmt := range block.Statements {
		diags = append(diags, checkStmtDomainBoundaries(stmt, contextName, line, col)...)
	}

	return diags
}

func checkStmtDomainBoundaries(stmt Statement, contextName string, line, col int) []Diagnostic {
	var diags []Diagnostic
	if isNil(stmt) {
		return diags
	}

	switch s := stmt.(type) {
	case *BlockStmt:
		diags = append(diags, checkBlockDomainBoundaries(s, contextName, line, col)...)
	case *LetStmt:
		diags = append(diags, checkExprDomainBoundaries(s.Value, contextName, line, col)...)
	case *ReturnStmt:
		diags = append(diags, checkExprDomainBoundaries(s.Value, contextName, line, col)...)
	case *ExprStmt:
		diags = append(diags, checkExprDomainBoundaries(s.Value, contextName, line, col)...)
	case *IfStmt:
		diags = append(diags, checkExprDomainBoundaries(s.Condition, contextName, line, col)...)
		diags = append(diags, checkStmtDomainBoundaries(s.Body, contextName, line, col)...)
		diags = append(diags, checkStmtDomainBoundaries(s.ElseBody, contextName, line, col)...)
	case *ForStmt:
		diags = append(diags, checkStmtDomainBoundaries(s.Body, contextName, line, col)...)
	case *TryCatchStmt:
		diags = append(diags, checkStmtDomainBoundaries(s.TryBody, contextName, line, col)...)
		diags = append(diags, checkStmtDomainBoundaries(s.CatchBody, contextName, line, col)...)
	case *MatchStmt:
		for _, c := range s.Cases {
			diags = append(diags, checkStmtDomainBoundaries(c.Body, contextName, line, col)...)
		}
	}

	return diags
}

func checkExprDomainBoundaries(expr Expression, contextName string, line, col int) []Diagnostic {
	var diags []Diagnostic
	if isNil(expr) {
		return diags
	}

	switch e := expr.(type) {
	case *CallExpr:
		if ident, ok := e.Function.(*Identifier); ok {
			fnName := ident.Value
			if strings.HasPrefix(fnName, "auth_private_") && !strings.Contains(contextName, "auth") {
				diags = append(diags, Diagnostic{
					Line:     line,
					Col:      col,
					Severity: "warning",
					Message:  fmt.Sprintf("Domain boundary violation: handler for '%s' directly invokes internal helper '%s' belonging to another domain context.", contextName, fnName),
				})
			}
			if strings.HasPrefix(fnName, "mail_internal_") && !strings.Contains(contextName, "mail") {
				diags = append(diags, Diagnostic{
					Line:     line,
					Col:      col,
					Severity: "warning",
					Message:  fmt.Sprintf("Domain boundary violation: handler for '%s' directly invokes internal helper '%s' belonging to another domain context.", contextName, fnName),
				})
			}
		}

		for _, arg := range e.Arguments {
			diags = append(diags, checkExprDomainBoundaries(arg, contextName, line, col)...)
		}

	case *InfixExpr:
		diags = append(diags, checkExprDomainBoundaries(e.Left, contextName, line, col)...)
		diags = append(diags, checkExprDomainBoundaries(e.Right, contextName, line, col)...)

	case *PrefixExpr:
		diags = append(diags, checkExprDomainBoundaries(e.Right, contextName, line, col)...)

	case *IndexExpr:
		diags = append(diags, checkExprDomainBoundaries(e.Left, contextName, line, col)...)
		diags = append(diags, checkExprDomainBoundaries(e.Index, contextName, line, col)...)

	case *MemberExpr:
		diags = append(diags, checkExprDomainBoundaries(e.Object, contextName, line, col)...)

	case *OptionalMemberExpr:
		diags = append(diags, checkExprDomainBoundaries(e.Object, contextName, line, col)...)

	case *MapLiteral:
		for _, val := range e.Pairs {
			diags = append(diags, checkExprDomainBoundaries(val, contextName, line, col)...)
		}

	case *ArrayLiteral:
		for _, el := range e.Elements {
			diags = append(diags, checkExprDomainBoundaries(el, contextName, line, col)...)
		}
	}

	return diags
}
