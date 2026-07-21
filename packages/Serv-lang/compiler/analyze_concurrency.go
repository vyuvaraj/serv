package compiler

import (
	"fmt"
)

// analyzeConcurrencyGuardrails scans AST for unsafe concurrency patterns in spawn blocks.
func analyzeConcurrencyGuardrails(program *Program) []Diagnostic {
	var diags []Diagnostic

	for _, stmt := range program.Statements {
		walkStmtConcurrency(stmt, make(map[string]bool), &diags)
	}

	return diags
}

func walkStmtConcurrency(stmt Statement, outerLocals map[string]bool, diags *[]Diagnostic) {
	if stmt == nil {
		return
	}

	switch s := stmt.(type) {
	case *LetStmt:
		outerLocals[s.Name] = true
		if s.Value != nil {
			walkExprConcurrency(s.Value, outerLocals, diags)
		}

	case *SpawnStmt:
		if s.Call != nil {
			walkExprConcurrency(s.Call, outerLocals, diags)
			if fnLit, ok := s.Call.(*FnLiteral); ok && fnLit.Body != nil {
				checkSpawnBodyConcurrency(fnLit.Body, outerLocals, diags)
			}
		}

	case *BlockStmt:
		if s != nil {
			localScope := copyConcurrencySymbolMap(outerLocals)
			for _, child := range s.Statements {
				walkStmtConcurrency(child, localScope, diags)
			}
		}

	case *FnDecl:
		if s.Body != nil {
			localScope := make(map[string]bool)
			for _, paramName := range s.Params {
				localScope[paramName] = true
			}
			walkStmtConcurrency(s.Body, localScope, diags)
		}

	case *RouteStmt:
		if s.Body != nil {
			localScope := make(map[string]bool)
			if s.Param != "" {
				localScope[s.Param] = true
			}
			walkStmtConcurrency(s.Body, localScope, diags)
		}

	case *SubscribeStmt:
		if s.Body != nil {
			localScope := make(map[string]bool)
			if s.Param != "" {
				localScope[s.Param] = true
			}
			walkStmtConcurrency(s.Body, localScope, diags)
		}

	case *TransformStmt:
		if s.Body != nil {
			localScope := make(map[string]bool)
			if s.Param != "" {
				localScope[s.Param] = true
			}
			walkStmtConcurrency(s.Body, localScope, diags)
		}

	case *ExprStmt:
		if s.Value != nil {
			walkExprConcurrency(s.Value, outerLocals, diags)
		}
	}
}

func checkSpawnBodyConcurrency(block *BlockStmt, outerLocals map[string]bool, diags *[]Diagnostic) {
	if block == nil {
		return
	}

	for _, stmt := range block.Statements {
		switch s := stmt.(type) {
		case *LetStmt:
			if outerLocals[s.Name] {
				*diags = append(*diags, Diagnostic{
					Line:     s.Token.Line,
					Col:      0,
					Severity: "warning",
					Message:  fmt.Sprintf("concurrent mutation of outer variable '%s' inside spawn block: potential race condition", s.Name),
				})
			}
		case *ExprStmt:
			if assign, ok := s.Value.(*AssignExpr); ok {
				if outerLocals[assign.Name] {
					*diags = append(*diags, Diagnostic{
						Line:     s.Token.Line,
						Col:      0,
						Severity: "warning",
						Message:  fmt.Sprintf("concurrent mutation of outer variable '%s' inside spawn block: potential race condition", assign.Name),
					})
				}
			}
		}
	}
}

func walkExprConcurrency(expr Expression, outerLocals map[string]bool, diags *[]Diagnostic) {
	if expr == nil {
		return
	}

	if fnLit, ok := expr.(*FnLiteral); ok && fnLit.Body != nil {
		walkStmtConcurrency(fnLit.Body, outerLocals, diags)
	}
}

func copyConcurrencySymbolMap(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
