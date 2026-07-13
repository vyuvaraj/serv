package compiler

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)


func (p *Parser) parseBrokerStatement() Statement {
	stmt := &BrokerStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseAiStatement() Statement {
	stmt := &AiStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseAppStatement() Statement {
	stmt := &AppStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) && !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseAgentDeclaration() Statement {
	stmt := &AgentDecl{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	p.nextToken()
	for p.curToken.Type != TOKEN_RBRACE && p.curToken.Type != TOKEN_EOF {
		if p.curToken.Type == TOKEN_IDENT {
			key := p.curToken.Literal
			if !p.expectPeek(TOKEN_COLON) {
				return nil
			}
			p.nextToken()
			valExpr := p.parseExpression(LOWEST)
			valStr := ""
			if lit, ok := valExpr.(*StringLiteral); ok {
				valStr = lit.Value
			} else if lit, ok := valExpr.(*Identifier); ok {
				valStr = lit.Value
			} else if list, ok := valExpr.(*ArrayLiteral); ok {
				// Parse array values for tools: tools: [add, multiply]
				for _, el := range list.Elements {
					if ident, ok := el.(*Identifier); ok {
						stmt.Tools = append(stmt.Tools, ident.Value)
					} else if str, ok := el.(*StringLiteral); ok {
						stmt.Tools = append(stmt.Tools, str.Value)
					}
				}
			}

			switch key {
			case "system":
				stmt.System = valStr
			case "model":
				stmt.Model = valStr
			}
		}
		p.nextToken()
	}
	return stmt
}

func (p *Parser) parseAuthStatement() Statement {
	stmt := &AuthStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseMailStatement() Statement {
	stmt := &MailStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseNotifyStatement() Statement {
	stmt := &NotifyStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseStoreStatement() Statement {
	stmt := &StoreStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseSearchStatement() Statement {
	stmt := &SearchStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}




func (p *Parser) parseServerStatement() Statement {
	stmt := &ServerStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)

	// Optional TLS: server "8080" tls "cert.pem" "key.pem"
	if p.peekToken.Type == TOKEN_IDENT && p.peekToken.Literal == "tls" {
		p.nextToken() // consume 'tls'
		if !p.expectPeek(TOKEN_STRING) {
			return nil
		}
		stmt.TLS = true
		stmt.CertFile = p.curToken.Literal
		if !p.expectPeek(TOKEN_STRING) {
			return nil
		}
		stmt.KeyFile = p.curToken.Literal
	}

	return stmt
}

func (p *Parser) parseRouteStatement() Statement {
	stmt := &RouteStmt{Token: p.curToken}

	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Method = p.curToken.Literal

	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Path = p.curToken.Literal
	if p.currentVersionPrefix != "" {
		pfx := p.currentVersionPrefix
		pth := stmt.Path
		if !strings.HasPrefix(pfx, "/") {
			pfx = "/" + pfx
		}
		pfx = strings.TrimSuffix(pfx, "/")
		if !strings.HasPrefix(pth, "/") {
			pth = "/" + pth
		}
		stmt.Path = pfx + pth
	}

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Param = p.curToken.Literal

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}

	if p.peekToken.Type == TOKEN_LIMIT {
		p.nextToken() // move to limit
		if !p.expectPeek(TOKEN_INT) {
			return nil
		}
		val, err := strconv.Atoi(p.curToken.Literal)
		if err != nil {
			return nil
		}
		stmt.LimitRate = val

		if !p.expectPeek(TOKEN_SLASH) {
			return nil
		}

		if !p.expectPeek(TOKEN_IDENT) {
			return nil
		}
		stmt.LimitPeriod = p.curToken.Literal
	}

	// Optional middleware: use [auth, logging]
	if p.peekToken.Type == TOKEN_USE {
		p.nextToken() // consume 'use'
		if !p.expectPeek(TOKEN_LBRACKET) {
			return nil
		}
		stmt.Middlewares = []string{}
		for p.peekToken.Type != TOKEN_RBRACKET && p.peekToken.Type != TOKEN_EOF {
			p.nextToken()
			expr := p.parseExpression(LOWEST)
			if expr != nil {
				stmt.Middlewares = append(stmt.Middlewares, expr.String())
			}
			if p.peekToken.Type == TOKEN_COMMA {
				p.nextToken()
			}
		}
		if !p.expectPeek(TOKEN_RBRACKET) {
			return nil
		}
	}

	for p.peekToken.Type == TOKEN_STREAM || p.peekToken.Type == TOKEN_RET_ARROW {
		if p.peekToken.Type == TOKEN_STREAM {
			p.nextToken()
			stmt.Stream = true
		} else if p.peekToken.Type == TOKEN_RET_ARROW {
			p.nextToken() // consume '->'
			p.nextToken() // type identifier
			stmt.ReturnType = p.parseTypeAnnotation()
		}
	}

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseToolStatement() Statement {
	stmt := &ToolStmt{Token: p.curToken}

	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Description = p.curToken.Literal

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Param = p.curToken.Literal

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseMigrationStatement() Statement {
	stmt := &MigrationStmt{Token: p.curToken}

	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Body = p.parseBlockStatement()

	// Parse table schemas from db.query("CREATE TABLE ...") inside the migration body
	stmt.Tables = []DBTable{}
	for _, blockStmt := range stmt.Body.Statements {
		exprStmt, ok := blockStmt.(*ExprStmt)
		if !ok {
			continue
		}
		callExpr, ok := exprStmt.Value.(*CallExpr)
		if !ok {
			continue
		}
		memberExpr, ok := callExpr.Function.(*MemberExpr)
		if !ok {
			continue
		}
		objIdent, ok := memberExpr.Object.(*Identifier)
		if !ok || objIdent.Value != "db" || (memberExpr.Field != "query" && memberExpr.Field != "querySafe") {
			continue
		}
		if len(callExpr.Arguments) < 1 {
			continue
		}
		strLit, ok := callExpr.Arguments[0].(*StringLiteral)
		if !ok {
			continue
		}

		sql := strings.TrimSpace(strLit.Value)
		sqlLower := strings.ToLower(sql)
		if strings.Contains(sqlLower, "create table") {
			// Extract Table Name and Columns
			// regex to parse CREATE TABLE [IF NOT EXISTS] table_name ( columns )
			reTableName := regexp.MustCompile(`(?i)create\s+table\s+(?:if\s+not\s+exists\s+)?([a-zA-Z0-9_]+)\s*\((.*)\)`)
			matches := reTableName.FindStringSubmatch(sql)
			if len(matches) >= 3 {
				tableName := matches[1]
				colDefsStr := matches[2]
				
				// Basic column definition parser (handles simple column lists separated by commas, skipping nested commas e.g. DECIMAL(10,2))
				// But sqlite/postgres migrations typically have simple types like INTEGER, TEXT, VARCHAR, REAL, BOOLEAN.
				colDefs := splitColumns(colDefsStr)
				
				table := DBTable{
					Name:    tableName,
					Columns: []DBColumn{},
				}

				for _, colDefStr := range colDefs {
					colDefStr = strings.TrimSpace(colDefStr)
					if colDefStr == "" {
						continue
					}
					// Check for table constraints like PRIMARY KEY (col), UNIQUE (col), FOREIGN KEY etc.
					colDefLower := strings.ToLower(colDefStr)
					if strings.HasPrefix(colDefLower, "primary key") || strings.HasPrefix(colDefLower, "foreign key") || strings.HasPrefix(colDefLower, "unique") || strings.HasPrefix(colDefLower, "check") || strings.HasPrefix(colDefLower, "constraint") {
						continue
					}
					
					parts := regexp.MustCompile(`\s+`).Split(colDefStr, -1)
					if len(parts) >= 2 {
						colName := strings.Trim(parts[0], "`\"[]")
						colTypeRaw := strings.ToLower(parts[1])
						
						// Map SQL types to Serv/Go types (int, float, string, bool)
						colType := "string"
						if strings.Contains(colTypeRaw, "int") {
							colType = "int"
						} else if strings.Contains(colTypeRaw, "double") || strings.Contains(colTypeRaw, "float") || strings.Contains(colTypeRaw, "real") || strings.Contains(colTypeRaw, "numeric") || strings.Contains(colTypeRaw, "decimal") {
							colType = "float64"
						} else if strings.Contains(colTypeRaw, "bool") {
							colType = "bool"
						}
						
						table.Columns = append(table.Columns, DBColumn{
							Name: colName,
							Type: colType,
						})
					}
				}
				stmt.Tables = append(stmt.Tables, table)
			}
		}
	}

	return stmt
}

// splitColumns splits columns DDL by commas, ignoring commas inside parentheses like DECIMAL(10,2)
func splitColumns(colDefsStr string) []string {
	var cols []string
	var current strings.Builder
	parenDepth := 0
	for i := 0; i < len(colDefsStr); i++ {
		char := colDefsStr[i]
		switch char {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
		}
		
		if char == ',' && parenDepth == 0 {
			cols = append(cols, current.String())
			current.Reset()
		} else {
			current.WriteByte(char)
		}
	}
	if current.Len() > 0 {
		cols = append(cols, current.String())
	}
	return cols
}

// parseTableDeclaration parses a declarative table schema:
//
//	table users {
//	    id        int      @primary @autoincrement
//	    name      string   @required
//	    email     string   @unique
//	    role      string   @default("user")
//	    createdAt datetime @default(now)
//	}
//
// Newlines are transparent (skipped by lexer). Columns are separated implicitly.
// The parser reads (name, type, [@annotation]*) until it sees TOKEN_RBRACE.
func (p *Parser) parseTableDeclaration() Statement {
	stmt := &TableDecl{Token: p.curToken}

	// table <name>
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	// opening {
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	p.nextToken() // move past {

	for p.curToken.Type != TOKEN_RBRACE && p.curToken.Type != TOKEN_EOF {
		// column name must be an identifier (or keyword-as-name)
		if p.curToken.Type == TOKEN_RBRACE || p.curToken.Type == TOKEN_EOF {
			break
		}
		colName := p.curToken.Literal
		p.nextToken()

		// column type
		if p.curToken.Type == TOKEN_RBRACE || p.curToken.Type == TOKEN_EOF {
			// orphan name — just stop
			break
		}
		colType := p.curToken.Literal
		p.nextToken()

		col := ColumnDef{
			Name: colName,
			Type: colType,
		}

		// Parse @annotations — TOKEN_MACRO is the '@' character
		for p.curToken.Type == TOKEN_MACRO {
			p.nextToken() // move past '@'
			if p.curToken.Type == TOKEN_EOF || p.curToken.Type == TOKEN_RBRACE {
				break
			}
			annotation := strings.ToLower(p.curToken.Literal)
			switch annotation {
			case "primary":
				col.Primary = true
				p.nextToken()
			case "autoincrement":
				col.AutoIncrement = true
				p.nextToken()
			case "required":
				col.Required = true
				p.nextToken()
			case "unique":
				col.Unique = true
				p.nextToken()
			case "default":
				p.nextToken() // past 'default'
				if p.curToken.Type == TOKEN_LPAREN {
					p.nextToken() // past '('
					defVal := p.curToken.Literal
					p.nextToken() // past value
					if p.curToken.Type == TOKEN_RPAREN {
						p.nextToken() // past ')'
					}
					col.Default = &defVal
				}
			default:
				p.nextToken()
			}
		}

		stmt.Columns = append(stmt.Columns, col)
	}

	return stmt
}






func (p *Parser) parseEveryStatement() Statement {
	stmt := &EveryStmt{Token: p.curToken}
	p.nextToken()
	stmt.Interval = p.parseExpression(LOWEST)

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseCronStatement() Statement {
	stmt := &CronStmt{Token: p.curToken}
	p.nextToken()
	stmt.Cron = p.parseExpression(LOWEST)

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseSubscribeStatement() Statement {
	stmt := &SubscribeStmt{Token: p.curToken}
	p.nextToken()
	stmt.Topic = p.parseExpression(LOWEST)

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Param = p.curToken.Literal

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseWsStatement parses: ws "/path" (conn) { body }
func (p *Parser) parseWsStatement() Statement {
	stmt := &WsStmt{Token: p.curToken}

	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Path = p.curToken.Literal

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Param = p.curToken.Literal

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseDeclareStatement parses: declare module "pkg/path" { fn Name(params) -> type; ... }
func (p *Parser) parseDeclareStatement() Statement {
	stmt := &DeclareModuleStmt{Token: p.curToken}

	// Expect 'module'
	if !p.expectPeek(TOKEN_MODULE) {
		return nil
	}

	// Package path string
	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.PkgPath = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Functions = []DeclareModuleFunc{}
	for p.peekToken.Type != TOKEN_RBRACE && p.peekToken.Type != TOKEN_EOF {
		p.nextToken()
		if p.curToken.Type != TOKEN_FN {
			p.addError(fmt.Sprintf("expected 'fn' in declare module body, got %s", p.curToken.Type))
			return nil
		}

		// Check for fn! (multi-return / failable function)
		multiReturn := false
		if p.peekToken.Type == TOKEN_BANG {
			multiReturn = true
			p.nextToken() // consume '!'
		}

		if !p.expectPeek(TOKEN_IDENT) {
			return nil
		}
		fn := DeclareModuleFunc{Name: p.curToken.Literal, MultiReturn: multiReturn}

		if !p.expectPeek(TOKEN_LPAREN) {
			return nil
		}

		fn.Params = []string{}
		fn.ParamTypes = []string{}
		if p.peekToken.Type != TOKEN_RPAREN {
			p.nextToken()
			fn.Params = append(fn.Params, p.curToken.Literal)
			if p.peekToken.Type == TOKEN_COLON {
				p.nextToken()
				p.nextToken()
				fn.ParamTypes = append(fn.ParamTypes, p.curToken.Literal)
			} else {
				fn.ParamTypes = append(fn.ParamTypes, "")
			}
			for p.peekToken.Type == TOKEN_COMMA {
				p.nextToken()
				p.nextToken()
				fn.Params = append(fn.Params, p.curToken.Literal)
				if p.peekToken.Type == TOKEN_COLON {
					p.nextToken()
					p.nextToken()
					fn.ParamTypes = append(fn.ParamTypes, p.curToken.Literal)
				} else {
					fn.ParamTypes = append(fn.ParamTypes, "")
				}
			}
		}

		if !p.expectPeek(TOKEN_RPAREN) {
			return nil
		}

		if p.peekToken.Type == TOKEN_RET_ARROW {
			p.nextToken()
			p.nextToken()
			fn.ReturnType = p.curToken.Literal
		}

		stmt.Functions = append(stmt.Functions, fn)
	}

	if !p.expectPeek(TOKEN_RBRACE) {
		return nil
	}

	return stmt
}

// parseMiddlewareDeclaration parses: middleware name(req) { body }
func (p *Parser) parseMiddlewareDeclaration() Statement {
	stmt := &MiddlewareDecl{Token: p.curToken}

	// Allow keywords (auth, mail, search, etc.) as middleware names
	p.nextToken()
	if p.curToken.Type != TOKEN_IDENT && !isKeywordToken(p.curToken.Type) {
		p.errors = append(p.errors, fmt.Sprintf("expected middleware name, got %s", p.curToken.Type))
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Param = p.curToken.Literal

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseDatabaseStatement() Statement {
	stmt := &DatabaseStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

func (p *Parser) parseCacheStatement() Statement {
	stmt := &CacheStmt{Token: p.curToken}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	return stmt
}

// parseExportStatement parses: export <statement>
// Wraps the inner statement in an ExportStmt node.
func (p *Parser) parseExportStatement() Statement {
	exportToken := p.curToken
	p.nextToken() // move past 'export' to the actual statement

	inner := p.parseStatement()
	if inner == nil {
		return nil
	}

	return &ExportStmt{Token: exportToken, Inner: inner}
}

func (p *Parser) parseCorsStatement() Statement {
	stmt := &CorsStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	if !p.expectPeek(TOKEN_IDENT) || p.curToken.Literal != "origins" {
		p.addError("expected 'origins' keyword in cors block")
		return nil
	}

	if !p.expectPeek(TOKEN_COLON) {
		return nil
	}

	p.nextToken()
	switch p.curToken.Type {
	case TOKEN_LBRACKET:
		for p.peekToken.Type != TOKEN_RBRACKET && p.peekToken.Type != TOKEN_EOF {
			p.nextToken()
			if p.curToken.Type == TOKEN_STRING {
				stmt.Origins = append(stmt.Origins, p.curToken.Literal)
			}
			if p.peekToken.Type == TOKEN_COMMA {
				p.nextToken()
			}
		}
		if !p.expectPeek(TOKEN_RBRACKET) {
			return nil
		}
	case TOKEN_STRING:
		stmt.Origins = []string{p.curToken.Literal}
	default:
		p.addError("expected string or list of strings for cors origins")
		return nil
	}

	if !p.expectPeek(TOKEN_RBRACE) {
		return nil
	}
	return stmt
}

func (p *Parser) parseRateLimitStatement() Statement {
	stmt := &RateLimitStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_INT) {
		return nil
	}
	val, err := strconv.Atoi(p.curToken.Literal)
	if err != nil {
		return nil
	}
	stmt.LimitRate = val

	if !p.expectPeek(TOKEN_SLASH) {
		return nil
	}

	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.LimitPeriod = p.curToken.Literal
	return stmt
}

func (p *Parser) parseInjectStatement() Statement {
	stmt := &InjectStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal
	if !p.expectPeek(TOKEN_COLON) {
		return nil
	}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.InterfaceName = p.curToken.Literal
	return stmt
}

func (p *Parser) parseGraphQLStatement() Statement {
	stmt := &GraphQLStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Path = p.curToken.Literal
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

func (p *Parser) parseMacroStatement() Statement {
	stmt := &MacroStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	// Optional arguments: @derive(Serialize, Validate)
	if p.peekToken.Type == TOKEN_LPAREN {
		p.nextToken() // consume '('
		stmt.Args = []string{}
		if p.peekToken.Type != TOKEN_RPAREN {
			p.nextToken() // first arg
			stmt.Args = append(stmt.Args, p.curToken.Literal)
			for p.peekToken.Type == TOKEN_COMMA {
				p.nextToken() // consume ','
				p.nextToken() // next arg
				stmt.Args = append(stmt.Args, p.curToken.Literal)
			}
		}
		if !p.expectPeek(TOKEN_RPAREN) {
			return nil
		}
	}
	return stmt
}

// parseMeshStatement parses: mesh { ... }
func (p *Parser) parseMeshStatement() Statement {
	stmt := &MeshStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseOnStatement parses: on "topic" (event) { ... }
func (p *Parser) parseOnStatement() Statement {
	stmt := &OnStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Topic = p.curToken.Literal

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Param = p.curToken.Literal

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseLockStatement parses: lock keyExpression { ... }
func (p *Parser) parseLockStatement() Statement {
	stmt := &LockStmt{Token: p.curToken}
	p.nextToken() // consume 'lock'
	stmt.Key = p.parseExpression(LOWEST)

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseBucketStatement parses: bucket name { ... }
func (p *Parser) parseBucketStatement() Statement {
	stmt := &BucketStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseGateStatement parses: gate name { ... }
func (p *Parser) parseGateStatement() Statement {
	stmt := &GateStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseJobStatement parses: job name cronSpecOrEvery { ... }
func (p *Parser) parseJobStatement() Statement {
	stmt := &JobStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	p.nextToken() // move to cronSpec/Every string
	stmt.Spec = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseRagStatement parses: rag "servstore://docs" { ... }
func (p *Parser) parseRagStatement() Statement {
	stmt := &RagStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Source = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	stmt.Body = p.parseBlockStatement()
	return stmt
}

// parseEventStoreStatement parses: event_store "orders" { ... }
func (p *Parser) parseEventStoreStatement() Statement {
	stmt := &EventStoreStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}
	p.nextToken()

	for p.curToken.Type != TOKEN_RBRACE && p.curToken.Type != TOKEN_EOF {
		switch p.curToken.Type {
		case TOKEN_COMMAND:
			cmd := p.parseCommandDeclaration()
			if cmd != nil {
				stmt.Commands = append(stmt.Commands, cmd)
			}
			p.nextToken()
		case TOKEN_ON:
			on := p.parseOnStatement()
			if onStmt, ok := on.(*OnStmt); ok {
				stmt.Handlers = append(stmt.Handlers, onStmt)
			}
			p.nextToken()
		default:
			p.nextToken()
		}
	}
	return stmt
}

func (p *Parser) parseCommandDeclaration() *CommandDecl {
	cmd := &CommandDecl{Token: p.curToken}
	if !p.expectPeek(TOKEN_IDENT) {
		return nil
	}
	cmd.Name = p.curToken.Literal

	if !p.expectPeek(TOKEN_LPAREN) {
		return nil
	}

	// Parse parameters
	if p.peekToken.Type != TOKEN_RPAREN {
		p.nextToken()
		cmd.Params = append(cmd.Params, p.curToken.Literal)
		for p.peekToken.Type == TOKEN_COMMA {
			p.nextToken() // cur token is COMMA
			p.nextToken() // cur token is parameter IDENT
			cmd.Params = append(cmd.Params, p.curToken.Literal)
		}
	}

	if !p.expectPeek(TOKEN_RPAREN) {
		return nil
	}

	if !p.expectPeek(TOKEN_LBRACE) {
		return nil
	}

	cmd.Body = p.parseBlockStatement()
	return cmd
}

func (p *Parser) parseEmitStatement() Statement {
	stmt := &EmitStmt{Token: p.curToken}
	if !p.expectPeek(TOKEN_STRING) {
		return nil
	}
	stmt.Event = p.curToken.Literal

	p.nextToken() // move past event string
	stmt.Payload = p.parseExpression(LOWEST)

	return stmt
}




