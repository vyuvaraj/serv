package compiler

import (
	"bytes"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
)

type Codegen struct {
	program             *Program
	imports             map[string]bool
	declaredVars        *Scope
	varTypes            map[string]string // varName -> type
	inFunction          bool
	inConcurrentContext bool
	goExterns           map[string]string // fnName -> pkgPath
	testFuncs           []string          // collected test functions
	regexDecls          []string          // collected compiled regex variables
	structTypes         map[string]bool   // known struct type names
	structFields        map[string][]StructField // structName -> fields (for type tracking)
	funcReturnTypes     map[string]string // fnName -> return type
	funcParamTypes      map[string][]string // fnName -> param types
	goPackageAliases    map[string]string // alias -> Go package name (e.g. "uuid" -> "uuid")
	packageAliases      map[string]string // pkgPath -> alias
	declaredGoFuncs     map[string]string // "pkg:FuncName" -> "pkgname.FuncName"
	goMultiReturnFuncs  map[string]bool   // "pkgname.FuncName" -> true (multi-return)
	beforeEachBlocks    []string          // collected beforeEach bodies for tests
	afterEachBlocks     []string          // collected afterEach bodies for tests

	currentFn           *FnDecl
	currentRoute        *RouteStmt
	currentMiddleware   *MiddlewareDecl
	currentWs           *WsStmt
	currentTool         *ToolStmt
	currentMigration    *MigrationStmt
	currEventStore      string
	currentEvery        *EveryStmt
	currentCron         *CronStmt
	currentSubscribe    *SubscribeStmt
	currentActor        *ActorDecl
	actorFields         map[string]bool
	actors              map[string]*ActorDecl
	currentWorkflow     *WorkflowDecl
	dbTables            map[string]DBTable
	Filename            string
	blockRefs           []map[string]bool
}

func NewCodegen(program *Program) *Codegen {
	return &Codegen{
		program:             program,
		imports:             make(map[string]bool),
		declaredVars:        NewScope(nil),
		varTypes:     make(map[string]string),
		goExterns:    make(map[string]string),
		regexDecls:   []string{},
		structTypes:  make(map[string]bool),
		structFields: make(map[string][]StructField),
		funcReturnTypes: make(map[string]string),
		funcParamTypes:  make(map[string][]string),
		goPackageAliases: make(map[string]string),
		packageAliases:   make(map[string]string),
		declaredGoFuncs:  make(map[string]string),
		goMultiReturnFuncs: make(map[string]bool),
		actorFields: make(map[string]bool),
		actors: make(map[string]*ActorDecl),
		dbTables: make(map[string]DBTable),
	}
}


func (c *Codegen) RunPrePass() {
	if len(c.structTypes) > 0 {
		return
	}
	c.program = Optimize(c.program)
	AnalyzeMapConcurrency(c.program)

	for _, stmt := range c.program.Statements {
		switch s := stmt.(type) {
		case *StructDecl:
			c.structTypes[s.Name] = true
			c.structFields[s.Name] = s.Fields
		case *FnDecl:
			if s.ReturnType != "" {
				c.funcReturnTypes[s.Name] = s.ReturnType
			}
			if len(s.ParamTypes) > 0 {
				c.funcParamTypes[s.Name] = s.ParamTypes
			}
		case *ActorDecl:
			c.actors[s.Name] = s
		case *MigrationStmt:
			for _, table := range s.Tables {
				c.dbTables[table.Name] = table
			}
		case *TableDecl:
			// Register declarative table columns as DBTable entries
			dbCols := make([]DBColumn, 0, len(s.Columns))
			for _, col := range s.Columns {
				dbCols = append(dbCols, DBColumn{Name: col.Name, Type: col.Type})
			}
			c.dbTables[s.Name] = DBTable{Name: s.Name, Columns: dbCols}

		case *ExternFnStmt:
			c.extractGoExtern(s)
		case *BlockStmt:
			for _, stmt := range s.Statements {
				if ext, ok := stmt.(*ExternFnStmt); ok {
					c.extractGoExtern(ext)
				}
			}
		case *DeclareModuleStmt:
			c.extractDeclareModule(s)
		case *ExportStmt:
			switch inner := s.Inner.(type) {
			case *StructDecl:
				c.structTypes[inner.Name] = true
				c.structFields[inner.Name] = inner.Fields
			case *ActorDecl:
				c.actors[inner.Name] = inner
			case *MigrationStmt:
				for _, table := range inner.Tables {
					c.dbTables[table.Name] = table
				}
			case *FnDecl:
				if inner.ReturnType != "" {
					c.funcReturnTypes[inner.Name] = inner.ReturnType
				}
				if len(inner.ParamTypes) > 0 {
					c.funcParamTypes[inner.Name] = inner.ParamTypes
				}
			case *ExternFnStmt:
				c.extractGoExtern(inner)
			case *DeclareModuleStmt:
				c.extractDeclareModule(inner)
			}
		}
	}
}

func (c *Codegen) extractDeclareModule(s *DeclareModuleStmt) {
	pkgName := filepath.Base(s.PkgPath)
	for _, fn := range s.Functions {
		key := s.PkgPath + ":" + fn.Name
		c.declaredGoFuncs[key] = pkgName + "." + fn.Name
		if fn.MultiReturn {
			c.goMultiReturnFuncs[pkgName+"."+fn.Name] = true
		}
	}
}

func (c *Codegen) getPackageAlias(pkgPath string) string {
	if alias, ok := c.packageAliases[pkgPath]; ok {
		return alias
	}
	base := filepath.Base(pkgPath)
	var sb strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			sb.WriteRune(r)
		}
	}
	sanitizedBase := sb.String()
	if sanitizedBase == "" {
		sanitizedBase = "pkg"
	}

	used := false
	for _, a := range c.packageAliases {
		if a == sanitizedBase {
			used = true
			break
		}
	}

	alias := sanitizedBase
	if used {
		counter := 1
		for {
			candidate := fmt.Sprintf("%s_%d", sanitizedBase, counter)
			conflict := false
			for _, a := range c.packageAliases {
				if a == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				alias = candidate
				break
			}
			counter++
		}
	}
	c.packageAliases[pkgPath] = alias
	if alias == sanitizedBase {
		c.imports[fmt.Sprintf(`"%s"`, pkgPath)] = true
	} else {
		c.imports[fmt.Sprintf(`%s "%s"`, alias, pkgPath)] = true
	}
	return alias
}

func (c *Codegen) extractGoExtern(ext *ExternFnStmt) {
	if strings.HasPrefix(ext.Source, "go:") {
		// Format: "go:importPath:FunctionName"
		parts := strings.Split(strings.TrimPrefix(ext.Source, "go:"), ":")
		if len(parts) >= 2 {
			pkgPath := parts[0]
			funcPart := parts[1]

			pkgAlias := c.getPackageAlias(pkgPath)

			if strings.Contains(funcPart, ".") {
				c.goExterns[ext.Name] = pkgAlias + ":" + funcPart
			} else {
				c.goExterns[ext.Name] = pkgAlias + "." + funcPart
			}
		}
	}
}

func (c *Codegen) GenerateStatements(statements []Statement) (string, error) {
	var body bytes.Buffer
	for _, stmt := range statements {
		if tok := stmtToken(stmt); tok.Line > 0 {
			body.WriteString(fmt.Sprintf("// .srv line %d\n", tok.Line))
			filename := "main.srv"
			if c.Filename != "" {
				filename = filepath.Base(c.Filename)
			}
			body.WriteString(fmt.Sprintf("//line %s:%d\n", filename, tok.Line))
		}
		gen, err := c.genStatement(stmt)
		if err != nil {
			return "", err
		}
		body.WriteString(gen)
	}
	return body.String(), nil
}

func (c *Codegen) Generate() (string, error) {
	c.RunPrePass()

	// Check if there are any non-test statements that would use the runtime
	hasNonTestStmts := false
	for _, stmt := range c.program.Statements {
		if _, isTest := stmt.(*TestStmt); !isTest {
			hasNonTestStmts = true
			break
		}
	}

	// Only add runtime/time imports when there are actual service statements
	if hasNonTestStmts {
		c.imports[`"serv/runtime"`] = true
		c.imports[`"time"`] = true
	}

	// fmt and runtime are always needed by helper functions and main()
	c.imports[`"fmt"`] = true
	c.imports[`"serv/runtime"`] = true
	if len(c.dbTables) > 0 {
		c.imports[`"strings"`] = true
	}


	var body bytes.Buffer
	bodyStr, err := c.GenerateStatements(c.program.Statements)
	if err != nil {
		return "", err
	}
	body.WriteString(bodyStr)

	body.WriteString(c.GenerateORMHelpers())

	// Build final output with imports
	var out bytes.Buffer
	out.WriteString("// Code generated by Serv compiler. DO NOT EDIT.\n")
	out.WriteString("package main\n\n")

	if len(c.imports) > 0 {
		out.WriteString("import (\n")
		for imp := range c.imports {
			out.WriteString("\t")
			out.WriteString(imp)
			out.WriteString("\n")
		}
		out.WriteString(")\n\n")
	}

	// Blank identifier guards to prevent "imported and not used" errors
	if c.imports[`"time"`] {
		out.WriteString("var _ = time.Second // ensure time is used\n\n")
	}
	if c.imports[`"fmt"`] {
		out.WriteString("var _ = fmt.Sprintf // ensure fmt is used\n\n")
	}
	if c.imports[`"serv/runtime"`] {
		out.WriteString("var _ = runtime.Noop // ensure runtime is used\n\n")
	}
	if c.imports[`"strconv"`] {
		out.WriteString("var _ = strconv.Atoi // ensure strconv is used\n\n")
	}
	if c.imports[`"regexp"`] {
		out.WriteString("var _ = regexp.MustCompile // ensure regexp is used\n\n")
	}
	if c.imports[`"strings"`] {
		out.WriteString("var _ = strings.Join // ensure strings is used\n\n")
	}
	if c.imports["\"encoding/json\""] {
		out.WriteString("var _ = json.Marshal // ensure json is used\n\n")
	}

	// Pre-compiled regex variables
	if len(c.regexDecls) > 0 {
		for _, rDecl := range c.regexDecls {
			out.WriteString(rDecl)
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}

	out.WriteString(body.String())

	return out.String(), nil
}

func (c *Codegen) GenerateORMHelpers() string {
	if len(c.dbTables) == 0 {
		return ""
	}
	var dbHelperCode strings.Builder
	dbHelperCode.WriteString("\n// --- Database ORM Structures and Helpers ---\n")

	dbClientStructName := "dbClientStruct"
	dbHelperCode.WriteString(fmt.Sprintf("type %s struct {\n", dbClientStructName))
	for tableName := range c.dbTables {
		dbHelperCode.WriteString(fmt.Sprintf("\t%s *dbTableClient_%s\n", capitalizeFirst(tableName), tableName))
	}
	dbHelperCode.WriteString("}\n\n")
	dbHelperCode.WriteString(fmt.Sprintf("var db = &%s{\n", dbClientStructName))
	for tableName := range c.dbTables {
		dbHelperCode.WriteString(fmt.Sprintf("\t%s: &dbTableClient_%s{},\n", capitalizeFirst(tableName), tableName))
	}
	dbHelperCode.WriteString("}\n\n")

	for tableName, table := range c.dbTables {
		rowStructName := capitalizeFirst(tableName) + "Row"
		dbHelperCode.WriteString(fmt.Sprintf("type %s struct {\n", rowStructName))
		for _, col := range table.Columns {
			goType := toGoType(col.Type)
			dbHelperCode.WriteString(fmt.Sprintf("\t%s %s\n", capitalizeFirst(col.Name), goType))
		}
		dbHelperCode.WriteString("}\n\n")

		clientStructName := "dbTableClient_" + tableName
		dbHelperCode.WriteString(fmt.Sprintf("type %s struct{}\n\n", clientStructName))

		dbHelperCode.WriteString(fmt.Sprintf(`func (c *%s) Find(filter map[string]interface{}) ([]%s, error) {
	query := "SELECT * FROM %s"
	var args []interface{}
	if len(filter) > 0 {
		var clauses []string
		for k, v := range filter {
			clauses = append(clauses, k + " = ?")
			args = append(args, v)
		}
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	res := runtime.DBQuery(query, args...)
	if tuple, ok := res.([2]interface{}); ok && tuple[1] != nil {
		return nil, fmt.Errorf("%%v", tuple[1])
	}
	slice, ok := res.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid result format")
	}
	var rows []%s
	for _, item := range slice {
		sm, ok := item.(*runtime.SafeMap)
		if !ok {
			continue
		}
		var r %s
`, clientStructName, rowStructName, tableName, rowStructName, rowStructName))

		for _, col := range table.Columns {
			goType := toGoType(col.Type)
			dbHelperCode.WriteString(fmt.Sprintf("\t\tif val := sm.Get(%q); val != nil {\n", col.Name))
			switch goType {
			case "int":
				dbHelperCode.WriteString(fmt.Sprintf("\t\t\tr.%s = toInt(val)\n", capitalizeFirst(col.Name)))
			case "float64":
				dbHelperCode.WriteString(fmt.Sprintf("\t\t\tr.%s = toFloat64(val)\n", capitalizeFirst(col.Name)))
			case "bool":
				dbHelperCode.WriteString(fmt.Sprintf("\t\t\tr.%s = toBool(val)\n", capitalizeFirst(col.Name)))
			default:
				dbHelperCode.WriteString(fmt.Sprintf("\t\t\tr.%s = toString(val)\n", capitalizeFirst(col.Name)))
			}
			dbHelperCode.WriteString("\t\t}\n")
		}
		dbHelperCode.WriteString(`		rows = append(rows, r)
	}
	return rows, nil
}

`)

		dbHelperCode.WriteString(fmt.Sprintf(`func (c *%s) FindOne(filter map[string]interface{}) (*%s, error) {
	rows, err := c.Find(filter)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

`, clientStructName, rowStructName))

		var colNames []string
		var colPlaceholders []string
		var colValues []string
		for _, col := range table.Columns {
			colNames = append(colNames, col.Name)
			colPlaceholders = append(colPlaceholders, "?")
			colValues = append(colValues, fmt.Sprintf("row.%s", capitalizeFirst(col.Name)))
		}
		dbHelperCode.WriteString(fmt.Sprintf(`func (c *dbTableClient_%s) Insert(row *%s) error {
	query := "INSERT INTO %s (%s) VALUES (%s)"
	res := runtime.DBQuery(query, %s)
	if tuple, ok := res.([2]interface{}); ok && tuple[1] != nil {
		return fmt.Errorf("%%v", tuple[1])
	}
	return nil
}

`, tableName, rowStructName, tableName, strings.Join(colNames, ", "), strings.Join(colPlaceholders, ", "), strings.Join(colValues, ", ")))

		dbHelperCode.WriteString(fmt.Sprintf(`func (c *%s) Update(filter map[string]interface{}, update map[string]interface{}) error {
	if len(update) == 0 {
		return nil
	}
	query := "UPDATE %s SET "
	var args []interface{}
	var setClauses []string
	for k, v := range update {
		setClauses = append(setClauses, k + " = ?")
		args = append(args, v)
	}
	query += strings.Join(setClauses, ", ")
	if len(filter) > 0 {
		var whereClauses []string
		for k, v := range filter {
			whereClauses = append(whereClauses, k + " = ?")
			args = append(args, v)
		}
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}
	res := runtime.DBQuery(query, args...)
	if tuple, ok := res.([2]interface{}); ok && tuple[1] != nil {
		return fmt.Errorf("%%v", tuple[1])
	}
	return nil
}

`, clientStructName, tableName))

		dbHelperCode.WriteString(fmt.Sprintf(`func (c *%s) Delete(filter map[string]interface{}) error {
	query := "DELETE FROM %s"
	var args []interface{}
	if len(filter) > 0 {
		var clauses []string
		for k, v := range filter {
			clauses = append(clauses, k + " = ?")
			args = append(args, v)
		}
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	res := runtime.DBQuery(query, args...)
	if tuple, ok := res.([2]interface{}); ok && tuple[1] != nil {
		return fmt.Errorf("%%v", tuple[1])
	}
	return nil
}

`, clientStructName, tableName))
	}
	return dbHelperCode.String()
}


func (c *Codegen) genStatement(stmt Statement) (string, error) {
switch s := stmt.(type) {
case *ImportStmt:
return c.genImportStmt(s)
case *GoPackageImport:
return c.genGoPackageImport(s)
case *DeclareModuleStmt:
return c.genDeclareModuleStmt(s)
case *ExportStmt:
return c.genExportStmt(s)
case *ExternFnStmt:
return c.genExternFnStmt(s)
case *EnumStmt:
return c.genEnumStmt(s)
case *TypeAliasStmt:
return c.genTypeAliasStmt(s)
case *ValidateStmt:
return c.genValidateStmt(s)
case *IfStmt:
return c.genIfStmt(s)
case *ForStmt:
return c.genForStmt(s)
case *StructDecl:
return c.genStructDecl(s)
case *ActorDecl:
return c.genActorDecl(s)
case *WorkflowDecl:
return c.genWorkflowDecl(s)
case *MethodDecl:
return c.genMethodDecl(s)
case *InterfaceDecl:
return c.genInterfaceDecl(s)
case *BrokerStmt:
return c.genBrokerStmt(s)
case *AiStmt:
return c.genAiStmt(s)
	case *AppStmt:
		return c.genAppStmt(s)
	case *AgentDecl:
		return c.genAgentDecl(s)
	case *MailStmt:
		return c.genMailStmt(s)
	case *NotifyStmt:
		return c.genNotifyStmt(s)
	case *StoreStmt:
		return c.genStoreStmt(s)
	case *SearchStmt:
		return c.genSearchStmt(s)
	case *AuthStmt:
		return c.genAuthStmt(s)
	case *ServerStmt:
		return c.genServerStmt(s)
	case *CorsStmt:
		return c.genCorsStmt(s)
	case *RateLimitStmt:
		return c.genRateLimitStmt(s)
	case *DatabaseStmt:
		return c.genDatabaseStmt(s)
	case *CacheStmt:
		return c.genCacheStmt(s)
case *MatchStmt:
return c.genMatchStmt(s)
case *RouteStmt:
return c.genRouteStmt(s)
case *InjectStmt:
return c.genInjectStmt(s)
case *GraphQLStmt:
return c.genGraphQLStmt(s)
case *MacroStmt:
return c.genMacroStmt(s)
case *MiddlewareDecl:
return c.genMiddlewareDecl(s)
case *WsStmt:
return c.genWsStmt(s)
case *ToolStmt:
return c.genToolStmt(s)
case *MigrationStmt:
return c.genMigrationStmt(s)
case *TableDecl:
return c.genTableDecl(s)
case *EveryStmt:
return c.genEveryStmt(s)
case *CronStmt:
return c.genCronStmt(s)
case *SubscribeStmt:
return c.genSubscribeStmt(s)
case *PublishStmt:
return c.genPublishStmt(s)
	case *SpawnStmt:
		return c.genSpawnStmt(s)
	case *MockStmt:
		return c.genMockStmt(s)
	case *TestStmt:
		return c.genTestStmt(s)
case *DestructureLetStmt:
return c.genDestructureLetStmt(s)
case *LetStmt:
return c.genLetStmt(s)
case *MeshStmt:
return c.genMeshStmt(s)
case *OnStmt:
return c.genOnStmt(s)
case *LockStmt:
return c.genLockStmt(s)
case *BucketStmt:
return c.genBucketStmt(s)
case *GateStmt:
return c.genGateStmt(s)
case *JobStmt:
return c.genJobStmt(s)
case *RagStmt:
	return c.genRagStmt(s)
case *EmitStmt:
	return c.genEmitStmt(s)
case *EventStoreStmt:
	return c.genEventStoreStmt(s)
case *ReturnStmt:
return c.genReturnStmt(s)
case *YieldStmt:
return c.genYieldStmt(s)
case *FnDecl:
return c.genFnDecl(s)
case *TryCatchStmt:
return c.genTryCatchStmt(s)
case *BreakStmt:
return "break\n", nil
case *ContinueStmt:
return "continue\n", nil
case *BeforeEachStmt:
return c.genBeforeEachStmt(s)
case *AfterEachStmt:
return c.genAfterEachStmt(s)
	case *ExprStmt:
		return c.genExprStmt(s)
	case *GoInlineFnStmt:
		return c.genGoInlineFnStmt(s)
	case *BlockStmt:
		if !c.inFunction {
			var out bytes.Buffer
			for _, sub := range s.Statements {
				gen, err := c.genStatement(sub)
				if err != nil {
					return "", err
				}
				out.WriteString(gen)
			}
			return out.String(), nil
		}
		return c.genBlockStatement(s)
	default:
		return "", fmt.Errorf("unknown statement type: %T", stmt)
	}
}

func (c *Codegen) genBlockStatement(block *BlockStmt) (string, error) {
	oldInFunc := c.inFunction
	c.inFunction = true
	defer func() { c.inFunction = oldInFunc }()

	oldDeclared := c.declaredVars
	c.declaredVars = NewScope(c.declaredVars)

	refs := c.findReferencedIdentifiers(block)
	c.blockRefs = append(c.blockRefs, refs)

	oldVarTypes := c.varTypes
	c.varTypes = make(map[string]string)
	for k, v := range oldVarTypes {
		c.varTypes[k] = v
	}
	defer func() {
		c.declaredVars = oldDeclared
		c.varTypes = oldVarTypes
		c.blockRefs = c.blockRefs[:len(c.blockRefs)-1]
	}()

	var out bytes.Buffer
	out.WriteString("{\n")
	for _, s := range block.Statements {
		if tok := stmtToken(s); tok.Line > 0 {
			out.WriteString(fmt.Sprintf("\t// .srv line %d\n", tok.Line))
		}
		gen, err := c.genStatement(s)
		if err != nil {
			return "", err
		}
		lines := strings.Split(gen, "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				out.WriteString("\t")
				out.WriteString(line)
				out.WriteString("\n")
			}
		}
	}
	out.WriteString("}")
	return out.String(), nil
}

func (c *Codegen) GenerateFileHeader(fileImports map[string]bool) string {
	var out bytes.Buffer
	out.WriteString("// Code generated by Serv compiler. DO NOT EDIT.\n")
	out.WriteString("package main\n\n")

	if len(fileImports) > 0 {
		out.WriteString("import (\n")
		for imp := range fileImports {
			out.WriteString("\t")
			out.WriteString(imp)
			out.WriteString("\n")
		}
		out.WriteString(")\n\n")
	}

	if fileImports[`"time"`] {
		out.WriteString("var _ = time.Second // ensure time is used\n\n")
	}
	if fileImports[`"fmt"`] {
		out.WriteString("var _ = fmt.Sprintf // ensure fmt is used\n\n")
	}
	if fileImports[`"serv/runtime"`] {
		out.WriteString("var _ = runtime.Noop // ensure runtime is used\n\n")
	}
	if fileImports[`"strconv"`] {
		out.WriteString("var _ = strconv.Atoi // ensure strconv is used\n\n")
	}
	if fileImports[`"regexp"`] {
		out.WriteString("var _ = regexp.MustCompile // ensure regexp is used\n\n")
	}
	if fileImports[`"strings"`] {
		out.WriteString("var _ = strings.Join // ensure strings is used\n\n")
	}
	if fileImports[`"encoding/json"`] {
		out.WriteString("var _ = json.Marshal // ensure json is used\n\n")
	}
	return out.String()
}

func (c *Codegen) isVarReferenced(name string) bool {
	for i := len(c.blockRefs) - 1; i >= 0; i-- {
		if c.blockRefs[i][name] {
			return true
		}
	}
	return false
}

func (c *Codegen) findReferencedIdentifiers(node Node) map[string]bool {
	refs := make(map[string]bool)
	var walk func(n Node)
	walk = func(n Node) {
		if n == nil {
			return
		}
		val := reflect.ValueOf(n)
		if val.Kind() == reflect.Ptr && val.IsNil() {
			return
		}
		switch nd := n.(type) {
		case *Identifier:
			refs[nd.Value] = true
		case *LetStmt:
			walk(nd.Value)
		case *FnDecl:
			walk(nd.Body)
		case *BlockStmt:
			for _, stmt := range nd.Statements {
				walk(stmt)
			}
		case *RouteStmt:
			walk(nd.Body)
		case *ExprStmt:
			walk(nd.Value)
		case *PrefixExpr:
			walk(nd.Right)
		case *InfixExpr:
			walk(nd.Left)
			walk(nd.Right)
		case *CallExpr:
			walk(nd.Function)
			for _, arg := range nd.Arguments {
				walk(arg)
			}
		case *IfStmt:
			walk(nd.Condition)
			walk(nd.Body)
			walk(nd.ElseBody)
		case *ForStmt:
			walk(nd.Iterable)
			walk(nd.Body)
		case *ReturnStmt:
			walk(nd.Value)
		case *TryCatchStmt:
			walk(nd.TryBody)
			walk(nd.CatchBody)
		case *AssignExpr:
			walk(nd.Value)
		case *MemberAssignExpr:
			walk(nd.Object)
			walk(nd.Value)
		case *IndexAssignExpr:
			walk(nd.Left)
			walk(nd.Value)
		case *CompoundAssignExpr:
			refs[nd.Name] = true
			walk(nd.Value)
		case *MemberExpr:
			walk(nd.Object)
		case *IndexExpr:
			walk(nd.Left)
			walk(nd.Index)
		}
	}
	walk(node)
	return refs
}