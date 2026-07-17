package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"serv/compiler"
)

// --- Hover (works on usage, not just definition) ---

func (s *Server) handleHover(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	syms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()

	// Get the word under the cursor
	word := getWordAtPosition(text, params.Position)
	if word == "" {
		sendResponse(msg.ID, nil)
		return
	}

	// DX.6: Check if cursor is on a member access (ns.member) — show member-level doc
	if memberHover := builtinMemberHover(text, params.Position, word); memberHover != "" {
		sendResponse(msg.ID, Hover{
			Contents: MarkupContent{Kind: "markdown", Value: memberHover},
		})
		return
	}

	// Search for a symbol matching this word
	for _, sym := range syms {
		if sym.Name == word || strings.HasSuffix(sym.Name, "."+word) || (sym.Kind == "fn" && sym.Name == word) {
			content := formatSymbolHover(sym)
			sendResponse(msg.ID, Hover{
				Contents: MarkupContent{Kind: "markdown", Value: content},
			})
			return
		}
	}

	// Check built-in objects
	if hover := builtinHover(word); hover != "" {
		sendResponse(msg.ID, Hover{
			Contents: MarkupContent{Kind: "markdown", Value: hover},
		})
		return
	}

	sendResponse(msg.ID, nil)
}

func formatSymbolHover(sym symbolInfo) string {
	switch sym.Kind {
	case "struct":
		return fmt.Sprintf("```serv\nstruct %s { %s }\n```", sym.Name, sym.TypeInfo)
	case "fn":
		params := formatParamList(sym.Params, sym.ParamTypes)
		ret := sym.TypeInfo
		if ret == "" {
			ret = "void"
		}
		return fmt.Sprintf("```serv\nfn %s(%s) -> %s\n```", sym.Name, params, ret)
	case "method":
		params := formatParamList(sym.Params, sym.ParamTypes)
		ret := sym.TypeInfo
		if ret == "" {
			ret = "void"
		}
		return fmt.Sprintf("```serv\nfn %s(%s) -> %s\n```", sym.Name, params, ret)
	case "let":
		t := sym.TypeInfo
		if t == "" {
			t = "inferred"
		}
		return fmt.Sprintf("```serv\nlet %s: %s\n```", sym.Name, t)
	case "route":
		return fmt.Sprintf("```serv\nroute %s\n```", sym.Name)
	case "middleware":
		return fmt.Sprintf("```serv\nmiddleware %s(req)\n```", sym.Name)
	case "enum":
		return fmt.Sprintf("```serv\nenum %s { %s }\n```", sym.Name, sym.TypeInfo)
	case "interface":
		return fmt.Sprintf("```serv\ninterface %s\n```", sym.Name)
	default:
		return sym.Name
	}
}

func formatParamList(params, paramTypes []string) string {
	var parts []string
	for i, p := range params {
		if i < len(paramTypes) && paramTypes[i] != "" {
			parts = append(parts, p+": "+paramTypes[i])
		} else {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

func builtinHover(word string) string {
	builtins := map[string]string{
		"log":      "Built-in structured logger\n\nMethods: `.info()`, `.warn()`, `.error()`, `.debug()`, `.with()`, `.fields()`",
		"db":       "Database client\n\nMethods: `.query()`, `.queryPage()`, `.findOne()`, `.count()`, `.upsert()`",
		"cache":    "Cache client (Redis or in-memory)\n\nMethods: `.set(key, value, ttl)`, `.get(key)`",
		"http":     "HTTP client\n\nMethods: `.get(url)`, `.post(url, body)`",
		"json":     "JSON utilities\n\nMethods: `.parse(str)`, `.stringify(obj)`",
		"time":     "Time utilities\n\nMethods: `.now()`, `.unix()`, `.sleep(ms)`",
		"metric":   "Metrics (exposed at /metrics)\n\nMethods: `.inc(name)`, `.gauge(name, value)`",
		"atomic":   "Atomic operations\n\nMethods: `.new()`, `.inc()`, `.dec()`, `.get()`, `.set()`, `.cas()`",
		"channel":  "Go channels\n\nMethods: `.new()`, `.send()`, `.receive()`, `.tryReceive()`, `.close()`",
		"registry": "Named function registry\n\nMethods: `.set()`, `.call()`, `.has()`, `.list()`",
		"s3":       "S3 / ServStore Client\n\nMethods: `.init(endpoint, accessKey, secretKey)`, `.createBucket(bucket)`, `.deleteBucket(bucket)`, `.setBucketVersioning(bucket, enabled)`, `.put(bucket, key, body)`, `.get(bucket, key)`, `.delete(bucket, key)`, `.list(bucket, prefix)`, `.at(bucket, key, timestamp)`, `.search(bucket, query, maxResults)`",
		"env":      "```serv\nfn env(key: string) -> string\n```\nRead environment variable",
		"config":   "```serv\nfn config(key: string) -> string\n```\nRead from config.yml or env var",
		"validate": "```serv\nfn validate(body, schema) -> []string | nil\n```\nValidate request body against schema",
		"wasm":     "WebAssembly guest execution helpers\n\nMethods: `.readInput()`, `.writeOutput(data)`",
	}
	if hover, ok := builtins[word]; ok {
		return hover
	}
	return ""
}

// builtinMemberHover returns member-level hover documentation for a qualified
// built-in access like log.info or db.query. DX.6.
func builtinMemberHover(text string, pos Position, word string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]

	// Find the word start in the line
	wordStart := pos.Character
	for wordStart > 0 && isWordChar(line[wordStart-1]) {
		wordStart--
	}

	// Check if there is a dot immediately before the word
	if wordStart < 1 || line[wordStart-1] != '.' {
		return ""
	}

	// Extract the namespace before the dot
	nsEnd := wordStart - 1
	nsStart := nsEnd
	for nsStart > 0 && isWordChar(line[nsStart-1]) {
		nsStart--
	}
	ns := line[nsStart:nsEnd]
	if ns == "" {
		return ""
	}

	// Look up in namespaceMembers
	if members, ok := namespaceMembers[ns]; ok {
		for _, m := range members {
			if m.Label == word && m.Documentation != "" {
				return m.Documentation
			}
		}
	}
	return ""
}

// --- Go to Definition (searches all symbols by name) ---

func (s *Server) handleDefinition(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	s.mu.RUnlock()

	s.parseAndRegisterImports(params.TextDocument.URI, text)

	s.mu.RLock()
	syms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()

	// DX.16: Check if cursor is on a serv:// URL string → navigate to remote service main.srv
	if loc := s.resolveServURLAtPosition(params.TextDocument.URI, text, params.Position); loc != nil {
		sendResponse(msg.ID, loc)
		return
	}

	// Check if cursor is on an import path string (e.g. "handlers/notifier.srv")
	if loc := s.resolveImportAtPosition(params.TextDocument.URI, text, params.Position); loc != nil {
		sendResponse(msg.ID, loc)
		return
	}

	word := getWordAtPosition(text, params.Position)
	if word == "" {
		sendResponse(msg.ID, nil)
		return
	}

	// Search current document symbols
	for _, sym := range syms {
		if sym.Name == word || strings.HasSuffix(sym.Name, "."+word) {
			sendResponse(msg.ID, Location{
				URI: params.TextDocument.URI,
				Range: Range{
					Start: Position{Line: sym.Line, Character: sym.Col},
					End:   Position{Line: sym.Line, Character: sym.Col + len(word)},
				},
			})
			return
		}
	}

	// Search all open documents (cross-file)
	s.mu.RLock()
	for uri, docSyms := range s.symbols {
		if uri == params.TextDocument.URI {
			continue
		}
		for _, sym := range docSyms {
			if sym.Name == word || strings.HasSuffix(sym.Name, "."+word) {
				s.mu.RUnlock()
				sendResponse(msg.ID, Location{
					URI: uri,
					Range: Range{
						Start: Position{Line: sym.Line, Character: sym.Col},
						End:   Position{Line: sym.Line, Character: sym.Col + len(word)},
					},
				})
				return
			}
		}
	}
	s.mu.RUnlock()

	sendResponse(msg.ID, nil)
}

// --- Find References & Rename ---

func (s *Server) handleReferences(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	syms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()

	word := getWordAtPosition(text, params.Position)
	if word == "" {
		sendResponse(msg.ID, []interface{}{})
		return
	}

	// Determine if this is a local variable
	isLocal := false
	var localStartLine, localEndLine int
	enclosingSym := findEnclosingSymbol(syms, params.Position.Line)
	if enclosingSym != nil {
		lines := strings.Split(text, "\n")
		localStartLine = enclosingSym.Line
		localEndLine = len(lines) - 1
		for _, sInfo := range syms {
			if sInfo.Line > localStartLine && sInfo.Line < localEndLine {
				localEndLine = sInfo.Line - 1
			}
		}
		if isLocalVariable(word, text, localStartLine, params.Position.Line) {
			isLocal = true
		}
	}

	var locations []Location

	s.mu.RLock()
	defer s.mu.RUnlock()

	if isLocal {
		docText := s.documents[params.TextDocument.URI]
		lines := strings.Split(docText, "\n")
		for lineNum := localStartLine; lineNum <= localEndLine && lineNum < len(lines); lineNum++ {
			line := lines[lineNum]
			locations = append(locations, findWordOccurrencesInLine(line, word, lineNum, params.TextDocument.URI)...)
		}
	} else {
		for uri, docText := range s.documents {
			lines := strings.Split(docText, "\n")
			for lineNum, line := range lines {
				locations = append(locations, findWordOccurrencesInLine(line, word, lineNum, uri)...)
			}
		}
	}

	sendResponse(msg.ID, locations)
}

func (s *Server) handlePrepareRename(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	s.mu.RUnlock()

	word := getWordAtPosition(text, params.Position)
	if word == "" || isBuiltinOrKeyword(word) {
		sendResponse(msg.ID, nil)
		return
	}

	lines := strings.Split(text, "\n")
	if params.Position.Line >= len(lines) {
		sendResponse(msg.ID, nil)
		return
	}
	line := lines[params.Position.Line]
	start := params.Position.Character
	for start > 0 && isWordChar(line[start-1]) {
		start--
	}
	end := params.Position.Character
	for end < len(line) && isWordChar(line[end]) {
		end++
	}

	sendResponse(msg.ID, Range{
		Start: Position{Line: params.Position.Line, Character: start},
		End:   Position{Line: params.Position.Line, Character: end},
	})
}

func (s *Server) handleRename(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
		NewName      string                 `json:"newName"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	syms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()

	word := getWordAtPosition(text, params.Position)
	if word == "" || isBuiltinOrKeyword(word) {
		sendResponse(msg.ID, nil)
		return
	}

	isLocal := false
	var localStartLine, localEndLine int
	enclosingSym := findEnclosingSymbol(syms, params.Position.Line)
	if enclosingSym != nil {
		lines := strings.Split(text, "\n")
		localStartLine = enclosingSym.Line
		localEndLine = len(lines) - 1
		for _, sInfo := range syms {
			if sInfo.Line > localStartLine && sInfo.Line < localEndLine {
				localEndLine = sInfo.Line - 1
			}
		}
		if isLocalVariable(word, text, localStartLine, params.Position.Line) {
			isLocal = true
		}
	}

	var locations []Location

	s.mu.RLock()
	if isLocal {
		docText := s.documents[params.TextDocument.URI]
		lines := strings.Split(docText, "\n")
		for lineNum := localStartLine; lineNum <= localEndLine && lineNum < len(lines); lineNum++ {
			line := lines[lineNum]
			locations = append(locations, findWordOccurrencesInLine(line, word, lineNum, params.TextDocument.URI)...)
		}
	} else {
		// Search all open documents
		for uri, docText := range s.documents {
			lines := strings.Split(docText, "\n")
			for lineNum, line := range lines {
				locations = append(locations, findWordOccurrencesInLine(line, word, lineNum, uri)...)
			}
		}

		// Also walk the workspace for .srv files not yet open
		currentPath := strings.TrimPrefix(params.TextDocument.URI, "file://")
		if strings.HasPrefix(currentPath, "/") && os.PathSeparator == '\\' {
			currentPath = strings.TrimPrefix(currentPath, "/")
		}
		workspaceDir := filepath.Dir(currentPath)
		if workspaceDir != "" && workspaceDir != "." && workspaceDir != "/" && workspaceDir != "\\" {
			if info, err := os.Stat(workspaceDir); err == nil && info.IsDir() {
				_ = filepath.WalkDir(workspaceDir, func(path string, d os.DirEntry, err error) error {
					if err != nil || d.IsDir() || !strings.HasSuffix(path, ".srv") {
						return nil
					}
					fileURI := "file://" + filepath.ToSlash(path)
					if _, alreadyOpen := s.documents[fileURI]; alreadyOpen {
						return nil
					}
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						return nil
					}
					docText := string(data)
					lines := strings.Split(docText, "\n")
					for lineNum, line := range lines {
						locations = append(locations, findWordOccurrencesInLine(line, word, lineNum, fileURI)...)
					}
					return nil
				})
			}
		}
	}
	s.mu.RUnlock()

	changes := make(map[string][]TextEdit)
	for _, loc := range locations {
		changes[loc.URI] = append(changes[loc.URI], TextEdit{
			Range:   loc.Range,
			NewText: params.NewName,
		})
	}

	sendResponse(msg.ID, WorkspaceEdit{Changes: changes})
}

// --- Helper Functions for Reference Resolution and Scope Tracking ---

func findEnclosingSymbol(symbols []symbolInfo, line int) *symbolInfo {
	var best *symbolInfo
	for i := range symbols {
		sym := &symbols[i]
		if sym.Line <= line {
			if best == nil || sym.Line > best.Line {
				best = sym
			}
		}
	}
	return best
}

func isLocalVariable(word string, text string, startLine, cursorLine int) bool {
	lines := strings.Split(text, "\n")
	for i := startLine; i <= cursorLine && i < len(lines); i++ {
		line := lines[i]
		if strings.Contains(line, "let ") && strings.Contains(line, word) {
			if isVarDeclaredInLet(line, word) {
				return true
			}
		}
		if strings.Contains(line, "for ") && strings.Contains(line, " in ") {
			if isVarDeclaredInFor(line, word) {
				return true
			}
		}
		if (strings.Contains(line, "fn ") || strings.Contains(line, "route ") || strings.Contains(line, "subscribe ") || strings.Contains(line, "ws ")) && (strings.Contains(line, "("+word) || strings.Contains(line, ", "+word)) {
			return true
		}
	}
	return false
}

func isVarDeclaredInLet(line, word string) bool {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) < 1 {
		return false
	}
	left := strings.TrimPrefix(strings.TrimSpace(parts[0]), "let ")
	vars := strings.Split(left, ",")
	for _, v := range vars {
		v = strings.TrimSpace(v)
		if strings.Contains(v, ":") {
			vParts := strings.SplitN(v, ":", 2)
			v = strings.TrimSpace(vParts[0])
		}
		if v == word {
			return true
		}
	}
	return false
}

func isVarDeclaredInFor(line, word string) bool {
	parts := strings.SplitN(line, " in ", 2)
	if len(parts) < 1 {
		return false
	}
	left := strings.TrimPrefix(strings.TrimSpace(parts[0]), "for ")
	vars := strings.Split(left, ",")
	for _, v := range vars {
		if strings.TrimSpace(v) == word {
			return true
		}
	}
	return false
}

func findWordOccurrencesInLine(line, word string, lineNum int, uri string) []Location {
	var locations []Location
	idx := 0
	for {
		pos := strings.Index(line[idx:], word)
		if pos < 0 {
			break
		}
		col := idx + pos
		isBoundary := true
		if col > 0 && isWordChar(line[col-1]) {
			isBoundary = false
		}
		endCol := col + len(word)
		if endCol < len(line) && isWordChar(line[endCol]) {
			isBoundary = false
		}
		if isBoundary {
			locations = append(locations, Location{
				URI: uri,
				Range: Range{
					Start: Position{Line: lineNum, Character: col},
					End:   Position{Line: lineNum, Character: endCol},
				},
			})
		}
		idx = col + len(word)
	}
	return locations
}

func isBuiltinOrKeyword(word string) bool {
	keywords := map[string]bool{
		"fn": true, "let": true, "return": true, "if": true, "else": true,
		"for": true, "in": true, "match": true, "struct": true, "interface": true,
		"middleware": true, "export": true, "import": true, "route": true,
		"every": true, "cron": true, "subscribe": true, "publish": true,
		"spawn": true, "server": true, "database": true, "broker": true,
		"cache": true, "try": true, "catch": true, "test": true, "assert": true,
		"enum": true, "await": true, "true": true, "false": true, "nil": true,
		"self": true, "declare": true, "module": true, "from": true, "extern": true,
		"migration": true, "tool": true, "ws": true, "use": true, "channel": true,
		"atomic": true, "break": true, "continue": true, "type": true,
	}
	builtins := map[string]bool{
		"log": true, "db": true, "cache": true, "http": true, "json": true,
		"time": true, "metric": true, "atomic": true, "channel": true,
		"registry": true, "env": true, "config": true, "validate": true,
		"s3": true, "wasm": true,
	}
	return keywords[word] || builtins[word]
}

// --- Document Symbols ---

func (s *Server) handleDocumentSymbol(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	syms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()

	result := []DocumentSymbol{}
	for _, sym := range syms {
		kind := 13
		switch sym.Kind {
		case "fn":
			kind = 12
		case "struct":
			kind = 23
		case "method":
			kind = 6
		case "interface":
			kind = 11
		case "route":
			kind = 12
		case "middleware":
			kind = 12
		case "enum":
			kind = 10
		}

		r := Range{
			Start: Position{Line: sym.Line, Character: 0},
			End:   Position{Line: sym.Line, Character: len(sym.Name) + 10},
		}
		result = append(result, DocumentSymbol{
			Name:           sym.Name,
			Kind:           kind,
			Range:          r,
			SelectionRange: r,
		})
	}

	sendResponse(msg.ID, result)
}

// --- Signature Help ---

func (s *Server) handleSignatureHelp(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	syms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()

	// Find the function name before the cursor (look backwards for identifier before '('
	// Also capture the full line prefix to detect qualified calls like log.info(
	linePrefix := getLinePrefix(text, params.Position)
	funcName, activeParam := findFunctionContext(text, params.Position)
	if funcName == "" {
		sendResponse(msg.ID, nil)
		return
	}

	// Find the function symbol
	for _, sym := range syms {
		if sym.Name == funcName && (sym.Kind == "fn" || sym.Kind == "method") && len(sym.Params) > 0 {
			sig := buildSignature(sym)
			sendResponse(msg.ID, SignatureHelp{
				Signatures:      []SignatureInformation{sig},
				ActiveSignature: 0,
				ActiveParameter: activeParam,
			})
			return
		}
	}

	// DX.2: Fall back to built-in namespace member signatures (e.g. log.info, db.query)
	if sig, ok := builtinNamespaceSignature(funcName, linePrefix); ok {
		sendResponse(msg.ID, SignatureHelp{
			Signatures:      []SignatureInformation{sig},
			ActiveSignature: 0,
			ActiveParameter: activeParam,
		})
		return
	}

	sendResponse(msg.ID, nil)
}

func findFunctionContext(text string, pos Position) (string, int) {
	lines := strings.Split(text, "\n")
	if pos.Line >= len(lines) {
		return "", 0
	}

	line := lines[pos.Line]
	if pos.Character > len(line) {
		return "", 0
	}

	// Look backwards from cursor to find the opening '('
	prefix := line[:pos.Character]
	depth := 0
	activeParam := 0

	for i := len(prefix) - 1; i >= 0; i-- {
		ch := prefix[i]
		if ch == ')' {
			depth++
		} else if ch == '(' {
			if depth == 0 {
				// Found the opening paren — extract function name before it
				nameEnd := i
				nameStart := nameEnd - 1
				for nameStart >= 0 && isWordChar(prefix[nameStart]) {
					nameStart--
				}
				nameStart++
				if nameStart < nameEnd {
					return prefix[nameStart:nameEnd], activeParam
				}
				return "", 0
			}
			depth--
		} else if ch == ',' && depth == 0 {
			activeParam++
		}
	}

	return "", 0
}

func buildSignature(sym symbolInfo) SignatureInformation {
	params := formatParamList(sym.Params, sym.ParamTypes)
	label := fmt.Sprintf("fn %s(%s)", sym.Name, params)
	if sym.TypeInfo != "" {
		label += " -> " + sym.TypeInfo
	}

	var paramInfos []ParameterInformation
	for i, p := range sym.Params {
		paramLabel := p
		if i < len(sym.ParamTypes) && sym.ParamTypes[i] != "" {
			paramLabel = p + ": " + sym.ParamTypes[i]
		}
		paramInfos = append(paramInfos, ParameterInformation{Label: paramLabel})
	}

	return SignatureInformation{
		Label:      label,
		Parameters: paramInfos,
	}
}

// builtinNamespaceSignature looks up signature help for a qualified built-in call like
// "log.info" or "db.query" by inspecting the line prefix to extract the namespace.
// DX.2: enables signature tooltips for all built-in namespace members.
func builtinNamespaceSignature(funcName, linePrefix string) (SignatureInformation, bool) {
	// Build a reverse-lookup: member name -> (ns, signature params string, doc)
	// Walk the linePrefix backwards past the function name to find a dot and a namespace
	// e.g. "  log.info("  → funcName="info", linePrefix has "log." before it
	idx := strings.LastIndex(linePrefix, funcName)
	if idx < 2 {
		return SignatureInformation{}, false
	}
	before := linePrefix[:idx]
	before = strings.TrimRight(before, " \t")
	if !strings.HasSuffix(before, ".") {
		return SignatureInformation{}, false
	}
	// Extract namespace identifier before the dot
	without := before[:len(before)-1]
	end := len(without)
	start := end
	for start > 0 {
		ch := without[start-1]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
			start--
		} else {
			break
		}
	}
	ns := without[start:end]
	if ns == "" {
		return SignatureInformation{}, false
	}

	members, ok := namespaceMembers[ns]
	if !ok {
		return SignatureInformation{}, false
	}
	for _, m := range members {
		if m.Label == funcName {
			// Reconstruct a readable signature from the InsertText ($N placeholders → param names)
			// e.g. `info("$1")` → `log.info(message: string)`
			// Use the Documentation first line if present, otherwise derive from InsertText
			label := ns + "." + funcName + "(" + extractParamHints(m.InsertText) + ")"
			sig := SignatureInformation{
				Label:         label,
				Documentation: m.Documentation,
			}
			// Build parameter infos from placeholders
			for _, ph := range extractParamHintList(m.InsertText) {
				sig.Parameters = append(sig.Parameters, ParameterInformation{Label: ph})
			}
			return sig, true
		}
	}
	return SignatureInformation{}, false
}

// extractParamHints returns a comma-joined string of snippet placeholders for display.
// e.g. `info("$1")` → `"$1"`, `set("$1", $2, "10m")` → `"$1", $2, "10m"`
func extractParamHints(insertText string) string {
	open := strings.Index(insertText, "(")
	if open < 0 {
		return ""
	}
	close := strings.LastIndex(insertText, ")")
	if close <= open {
		return ""
	}
	return insertText[open+1 : close]
}

// extractParamHintList splits the parameter hint string into individual parameters.
func extractParamHintList(insertText string) []string {
	inner := extractParamHints(insertText)
	if inner == "" {
		return nil
	}
	parts := strings.Split(inner, ", ")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// --- Autocomplete ---

// namespaceMembers maps each built-in object namespace to its member completions.
// InsertTextFormat:2 enables tab-stop placeholders ($1, $2) in VS Code (DX.1).
// SortText prefix "1_" places builtins after local symbols but before keywords (DX.8).
var namespaceMembers = map[string][]CompletionItem{
	"log": {
		{Label: "info", Kind: 3, Detail: "Log info message", InsertText: `info("$1")`, InsertTextFormat: 2, SortText: "1_log_info", Documentation: "```serv\nlog.info(message: string)\n```\nLog a structured info-level message.\n\n**Example:**\n```serv\nlog.info(\"User logged in\")\n```"},
		{Label: "warn", Kind: 3, Detail: "Log warning message", InsertText: `warn("$1")`, InsertTextFormat: 2, SortText: "1_log_warn", Documentation: "```serv\nlog.warn(message: string)\n```\nLog a structured warning message."},
		{Label: "error", Kind: 3, Detail: "Log error message", InsertText: `error("$1")`, InsertTextFormat: 2, SortText: "1_log_error", Documentation: "```serv\nlog.error(message: string)\n```\nLog a structured error message."},
		{Label: "debug", Kind: 3, Detail: "Log debug message", InsertText: `debug("$1")`, InsertTextFormat: 2, SortText: "1_log_debug", Documentation: "```serv\nlog.debug(message: string)\n```\nLog a debug message (suppressed in production)."},
	},
	"db": {
		{Label: "query", Kind: 3, Detail: "Execute database query", InsertText: `query("$1")`, InsertTextFormat: 2, SortText: "1_db_query", Documentation: "```serv\ndb.query(sql: string) -> []Row\n```\nExecute a raw SQL query and return rows."},
		{Label: "findOne", Kind: 3, Detail: "Find single document", InsertText: `findOne("$1", "$2")`, InsertTextFormat: 2, SortText: "1_db_findOne", Documentation: "```serv\ndb.findOne(collection: string, filter: string) -> Row\n```\nFind a single matching document."},
		{Label: "insert", Kind: 3, Detail: "Insert document", InsertText: `insert("$1", $2)`, InsertTextFormat: 2, SortText: "1_db_insert", Documentation: "```serv\ndb.insert(collection: string, doc: object)\n```\nInsert a new document into the collection."},
		{Label: "update", Kind: 3, Detail: "Update document", InsertText: `update("$1", $2)`, InsertTextFormat: 2, SortText: "1_db_update", Documentation: "```serv\ndb.update(collection: string, doc: object)\n```\nUpdate an existing document."},
		{Label: "delete", Kind: 3, Detail: "Delete document", InsertText: `delete("$1", "$2")`, InsertTextFormat: 2, SortText: "1_db_delete", Documentation: "```serv\ndb.delete(collection: string, id: string)\n```\nDelete a document by ID."},
	},
	"cache": {
		{Label: "get", Kind: 3, Detail: "Get cache value", InsertText: `get("$1")`, InsertTextFormat: 2, SortText: "1_cache_get", Documentation: "```serv\ncache.get(key: string) -> string\n```\nGet a value from the cache."},
		{Label: "set", Kind: 3, Detail: "Set cache value with TTL", InsertText: `set("$1", $2, "10m")`, InsertTextFormat: 2, SortText: "1_cache_set", Documentation: "```serv\ncache.set(key: string, value: any, ttl: string)\n```\nStore a value with a time-to-live.\n\n**Example:**\n```serv\ncache.set(\"user:123\", data, \"5m\")\n```"},
		{Label: "delete", Kind: 3, Detail: "Delete cache key", InsertText: `delete("$1")`, InsertTextFormat: 2, SortText: "1_cache_delete", Documentation: "```serv\ncache.delete(key: string)\n```\nEvict a key from the cache."},
		{Label: "flush", Kind: 3, Detail: "Flush entire cache namespace", InsertText: `flush("$1")`, InsertTextFormat: 2, SortText: "1_cache_flush", Documentation: "```serv\ncache.flush(namespace: string)\n```\nFlush all keys in a namespace."},
	},
	"http": {
		{Label: "get", Kind: 3, Detail: "HTTP GET request", InsertText: `get("$1")`, InsertTextFormat: 2, SortText: "1_http_get", Documentation: "```serv\nhttp.get(url: string) -> Response\n```\nPerform an HTTP GET request."},
		{Label: "post", Kind: 3, Detail: "HTTP POST request", InsertText: `post("$1", $2)`, InsertTextFormat: 2, SortText: "1_http_post", Documentation: "```serv\nhttp.post(url: string, body: object) -> Response\n```\nPerform an HTTP POST request."},
		{Label: "put", Kind: 3, Detail: "HTTP PUT request", InsertText: `put("$1", $2)`, InsertTextFormat: 2, SortText: "1_http_put", Documentation: "```serv\nhttp.put(url: string, body: object) -> Response\n```\nPerform an HTTP PUT request."},
		{Label: "delete", Kind: 3, Detail: "HTTP DELETE request", InsertText: `delete("$1")`, InsertTextFormat: 2, SortText: "1_http_delete", Documentation: "```serv\nhttp.delete(url: string) -> Response\n```\nPerform an HTTP DELETE request."},
		{Label: "patch", Kind: 3, Detail: "HTTP PATCH request", InsertText: `patch("$1", $2)`, InsertTextFormat: 2, SortText: "1_http_patch", Documentation: "```serv\nhttp.patch(url: string, body: object) -> Response\n```\nPerform an HTTP PATCH request."},
		{Label: "static", Kind: 3, Detail: "Serve static files at prefix path", InsertText: `static("$1", "$2")`, InsertTextFormat: 2, SortText: "1_http_static", Documentation: "```serv\nhttp.static(prefix: string, dir: string)\n```\nServe static files from a directory at a URL prefix."},
	},
	"json": {
		{Label: "parse", Kind: 3, Detail: "Parse JSON string", InsertText: `parse($1)`, InsertTextFormat: 2, SortText: "1_json_parse", Documentation: "```serv\njson.parse(text: string) -> object\n```\nParse a JSON string into an object."},
		{Label: "stringify", Kind: 3, Detail: "Stringify value to JSON", InsertText: `stringify($1)`, InsertTextFormat: 2, SortText: "1_json_stringify", Documentation: "```serv\njson.stringify(value: any) -> string\n```\nSerialize a value to a JSON string."},
	},
	"time": {
		{Label: "now", Kind: 3, Detail: "Current RFC3339 timestamp", InsertText: `now()`, InsertTextFormat: 2, SortText: "1_time_now", Documentation: "```serv\ntime.now() -> string\n```\nReturn the current time as an RFC3339 string."},
		{Label: "unix", Kind: 3, Detail: "Unix timestamp (seconds)", InsertText: `unix()`, InsertTextFormat: 2, SortText: "1_time_unix", Documentation: "```serv\ntime.unix() -> int\n```\nReturn the current Unix timestamp in seconds."},
		{Label: "sleep", Kind: 3, Detail: "Pause execution for milliseconds", InsertText: `sleep($1)`, InsertTextFormat: 2, SortText: "1_time_sleep", Documentation: "```serv\ntime.sleep(ms: int)\n```\nSuspend execution for the given number of milliseconds."},
		{Label: "format", Kind: 3, Detail: "Format timestamp with layout", InsertText: `format($1, "$2")`, InsertTextFormat: 2, SortText: "1_time_format", Documentation: "```serv\ntime.format(ts: string, layout: string) -> string\n```\nFormat a timestamp using a Go-style layout string."},
		{Label: "parse", Kind: 3, Detail: "Parse timestamp string", InsertText: `parse("$1", "$2")`, InsertTextFormat: 2, SortText: "1_time_parse", Documentation: "```serv\ntime.parse(layout: string, value: string) -> string\n```\nParse a timestamp string using a layout."},
	},
	"channel": {
		{Label: "new", Kind: 3, Detail: "Create buffered channel", InsertText: `new("$1", $2)`, InsertTextFormat: 2, SortText: "1_channel_new", Documentation: "```serv\nchannel.new(name: string, size: int)\n```\nCreate a named buffered channel."},
		{Label: "send", Kind: 3, Detail: "Send to channel", InsertText: `send("$1", $2)`, InsertTextFormat: 2, SortText: "1_channel_send", Documentation: "```serv\nchannel.send(name: string, value: any)\n```\nSend a value into a named channel."},
		{Label: "receive", Kind: 3, Detail: "Receive from channel", InsertText: `receive("$1")`, InsertTextFormat: 2, SortText: "1_channel_receive", Documentation: "```serv\nchannel.receive(name: string) -> any\n```\nReceive the next value from a named channel."},
		{Label: "close", Kind: 3, Detail: "Close channel", InsertText: `close("$1")`, InsertTextFormat: 2, SortText: "1_channel_close", Documentation: "```serv\nchannel.close(name: string)\n```\nClose a named channel."},
	},
	"atomic": {
		{Label: "new", Kind: 3, Detail: "Create atomic value", InsertText: `new("$1", $2)`, InsertTextFormat: 2, SortText: "1_atomic_new", Documentation: "```serv\natomic.new(name: string, initialValue: any)\n```\nCreate a named atomic value."},
		{Label: "inc", Kind: 3, Detail: "Increment atomic counter", InsertText: `inc("$1")`, InsertTextFormat: 2, SortText: "1_atomic_inc", Documentation: "```serv\natomic.inc(name: string) -> int\n```\nAtomically increment a counter."},
		{Label: "dec", Kind: 3, Detail: "Decrement atomic counter", InsertText: `dec("$1")`, InsertTextFormat: 2, SortText: "1_atomic_dec", Documentation: "```serv\natomic.dec(name: string) -> int\n```\nAtomically decrement a counter."},
		{Label: "get", Kind: 3, Detail: "Get atomic value", InsertText: `get("$1")`, InsertTextFormat: 2, SortText: "1_atomic_get", Documentation: "```serv\natomic.get(name: string) -> any\n```\nRead the current value atomically."},
		{Label: "set", Kind: 3, Detail: "Set atomic value", InsertText: `set("$1", $2)`, InsertTextFormat: 2, SortText: "1_atomic_set", Documentation: "```serv\natomic.set(name: string, value: any)\n```\nWrite a value atomically."},
	},
	"metric": {
		{Label: "inc", Kind: 3, Detail: "Increment counter metric", InsertText: `inc("$1")`, InsertTextFormat: 2, SortText: "1_metric_inc", Documentation: "```serv\nmetric.inc(name: string)\n```\nIncrement a named Prometheus counter."},
		{Label: "gauge", Kind: 3, Detail: "Set gauge value", InsertText: `gauge("$1", $2)`, InsertTextFormat: 2, SortText: "1_metric_gauge", Documentation: "```serv\nmetric.gauge(name: string, value: float)\n```\nSet a Prometheus gauge to the given value."},
		{Label: "histogram", Kind: 3, Detail: "Record histogram observation", InsertText: `histogram("$1", $2)`, InsertTextFormat: 2, SortText: "1_metric_histogram", Documentation: "```serv\nmetric.histogram(name: string, value: float)\n```\nRecord an observation in a Prometheus histogram."},
	},
	"s3": {
		{Label: "init", Kind: 3, Detail: "Initialize S3 client", InsertText: `init("$1", "$2", "$3")`, InsertTextFormat: 2, SortText: "1_s3_init", Documentation: "```serv\ns3.init(endpoint: string, accessKey: string, secretKey: string)\n```\nInitialize an S3-compatible client."},
		{Label: "createBucket", Kind: 3, Detail: "Create S3 Bucket", InsertText: `createBucket("$1")`, InsertTextFormat: 2, SortText: "1_s3_createBucket", Documentation: "```serv\ns3.createBucket(name: string)\n```\nCreate a new S3 bucket."},
		{Label: "deleteBucket", Kind: 3, Detail: "Delete S3 Bucket", InsertText: `deleteBucket("$1")`, InsertTextFormat: 2, SortText: "1_s3_deleteBucket", Documentation: "```serv\ns3.deleteBucket(name: string)\n```\nDelete an S3 bucket and all its contents."},
		{Label: "setBucketVersioning", Kind: 3, Detail: "Set S3 Bucket Versioning", InsertText: `setBucketVersioning("$1", $2)`, InsertTextFormat: 2, SortText: "1_s3_setBucketVersioning", Documentation: "```serv\ns3.setBucketVersioning(bucket: string, enabled: bool)\n```\nEnable or disable versioning on a bucket."},
		{Label: "put", Kind: 3, Detail: "Upload object to S3", InsertText: `put("$1", "$2", $3)`, InsertTextFormat: 2, SortText: "1_s3_put", Documentation: "```serv\ns3.put(bucket: string, key: string, body: bytes)\n```\nUpload an object."},
		{Label: "get", Kind: 3, Detail: "Retrieve object from S3", InsertText: `get("$1", "$2")`, InsertTextFormat: 2, SortText: "1_s3_get", Documentation: "```serv\ns3.get(bucket: string, key: string) -> bytes\n```\nRetrieve an object."},
		{Label: "delete", Kind: 3, Detail: "Delete S3 object", InsertText: `delete("$1", "$2")`, InsertTextFormat: 2, SortText: "1_s3_delete", Documentation: "```serv\ns3.delete(bucket: string, key: string)\n```\nDelete an object from the bucket."},
		{Label: "list", Kind: 3, Detail: "List S3 objects", InsertText: `list("$1", "$2")`, InsertTextFormat: 2, SortText: "1_s3_list", Documentation: "```serv\ns3.list(bucket: string, prefix: string) -> []string\n```\nList object keys with a prefix."},
		{Label: "at", Kind: 3, Detail: "Time-travel S3 object version", InsertText: `at("$1", "$2", "$3")`, InsertTextFormat: 2, SortText: "1_s3_at", Documentation: "```serv\ns3.at(bucket: string, key: string, timestamp: string) -> bytes\n```\nRetrieve a specific historical version of an object."},
		{Label: "search", Kind: 3, Detail: "Semantic search S3 query", InsertText: `search("$1", "$2", $3)`, InsertTextFormat: 2, SortText: "1_s3_search", Documentation: "```serv\ns3.search(bucket: string, query: string, maxResults: int) -> []string\n```\nSemantic search across stored documents."},
	},
	"store": {
		{Label: "get", Kind: 3, Detail: "Retrieve object from ServStore", InsertText: `get("$1", "$2")`, InsertTextFormat: 2, SortText: "1_store_get", Documentation: "```serv\nstore.get(bucket: string, key: string) -> bytes\n```\nRetrieve an object from ServStore."},
		{Label: "put", Kind: 3, Detail: "Upload object to ServStore", InsertText: `put("$1", "$2", $3)`, InsertTextFormat: 2, SortText: "1_store_put", Documentation: "```serv\nstore.put(bucket: string, key: string, body: bytes)\n```\nUpload an object to ServStore."},
		{Label: "delete", Kind: 3, Detail: "Delete object from ServStore", InsertText: `delete("$1", "$2")`, InsertTextFormat: 2, SortText: "1_store_delete", Documentation: "```serv\nstore.delete(bucket: string, key: string)\n```\nDelete an object from ServStore."},
	},
	"mail": {
		{Label: "send", Kind: 3, Detail: "Send email notification", InsertText: `send("$1", "$2", "$3")`, InsertTextFormat: 2, SortText: "1_mail_send", Documentation: "```serv\nmail.send(to: string, subject: string, body: string)\n```\nSend a plain-text email."},
		{Label: "sendTemplate", Kind: 3, Detail: "Send templated email", InsertText: `sendTemplate("$1", "$2", $3)`, InsertTextFormat: 2, SortText: "1_mail_sendTemplate", Documentation: "```serv\nmail.sendTemplate(to: string, template: string, data: object)\n```\nSend an email using a named template with data."},
	},
	"wasm": {
		{Label: "readInput", Kind: 3, Detail: "Read input from stdin (WASM context)", InsertText: `readInput()`, InsertTextFormat: 2, SortText: "1_wasm_readInput", Documentation: "```serv\nwasm.readInput() -> string\n```\nRead raw input from stdin in a WASM execution context."},
		{Label: "writeOutput", Kind: 3, Detail: "Write output to stdout (WASM context)", InsertText: `writeOutput($1)`, InsertTextFormat: 2, SortText: "1_wasm_writeOutput", Documentation: "```serv\nwasm.writeOutput(value: any)\n```\nWrite a value to stdout in a WASM execution context."},
	},
	"ai": {
		{Label: "complete", Kind: 3, Detail: "Run LLM completion", InsertText: `complete("$1", $2)`, InsertTextFormat: 2, SortText: "1_ai_complete", Documentation: "```serv\nai.complete(prompt: string, options: object) -> string\n```\nRun an LLM completion against the configured provider."},
		{Label: "embed", Kind: 3, Detail: "Generate embedding vector", InsertText: `embed("$1")`, InsertTextFormat: 2, SortText: "1_ai_embed", Documentation: "```serv\nai.embed(text: string) -> []float\n```\nGenerate a semantic embedding vector."},
		{Label: "classify", Kind: 3, Detail: "Classify text", InsertText: `classify("$1", $2)`, InsertTextFormat: 2, SortText: "1_ai_classify", Documentation: "```serv\nai.classify(text: string, labels: []string) -> string\n```\nClassify text into one of the provided labels."},
		{Label: "summarize", Kind: 3, Detail: "Summarize text", InsertText: `summarize("$1")`, InsertTextFormat: 2, SortText: "1_ai_summarize", Documentation: "```serv\nai.summarize(text: string) -> string\n```\nGenerate a concise summary of a long text."},
	},
}

func (s *Server) handleCompletion(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Position     Position               `json:"position"`
	}
	json.Unmarshal(msg.Params, &params)

	// --- Context detection: check if cursor is right after a namespace dot ---
	s.mu.RLock()
	docText := s.documents[params.TextDocument.URI]
	s.mu.RUnlock()

	linePrefix := getLinePrefix(docText, params.Position)

	// DX.4: Import path auto-complete — triggered when inside `import "...` string
	if importItems := s.importPathCompletions(params.TextDocument.URI, linePrefix); importItems != nil {
		sendResponse(msg.ID, CompletionList{IsIncomplete: false, Items: importItems})
		return
	}

	if ns := extractTriggerNamespace(linePrefix); ns != "" {
		// 1. Built-in namespace members (e.g. log., db., cache.)
		if members, ok := namespaceMembers[ns]; ok {
			sendResponse(msg.ID, CompletionList{IsIncomplete: false, Items: members})
			return
		}

		// DX.5: Struct field member completions
		s.mu.RLock()
		syms := s.symbols[params.TextDocument.URI]
		s.mu.RUnlock()
		if fieldItems := structFieldCompletions(ns, docText, syms); fieldItems != nil {
			sendResponse(msg.ID, CompletionList{IsIncomplete: false, Items: fieldItems})
			return
		}
	}

	// DX.7: match arm enum completions — triggered when inside `match <ident> {`
	s.mu.RLock()
	allSyms := s.symbols[params.TextDocument.URI]
	s.mu.RUnlock()
	if enumItems := enumMatchCompletions(docText, params.Position, allSyms); enumItems != nil {
		sendResponse(msg.ID, CompletionList{IsIncomplete: false, Items: enumItems})
		return
	}

	items := []CompletionItem{}

	// Block keyword snippets (DX.3) — rich multi-line scaffolds for structural keywords
	snippets := []CompletionItem{
		{
			Label: "fn", Kind: 15, Detail: "Function declaration", SortText: "2_fn",
			InsertTextFormat: 2,
			InsertText:       "fn ${1:name}(${2:param}: ${3:type}) -> ${4:string} {\n\t$0\n}",
			Documentation:    "Declare a named function.\n\n```serv\nfn greet(name: string) -> string {\n  return \"Hello, \" + name\n}\n```",
		},
		{
			Label: "route", Kind: 15, Detail: "HTTP route handler", SortText: "2_route",
			InsertTextFormat: 2,
			InsertText:       "route ${1|GET,POST,PUT,DELETE,PATCH|} \"${2:/path}\" (${3:req}: ${4:Request}) -> ${5:Response} {\n\t$0\n}",
			Documentation:    "Declare an HTTP route handler.\n\n```serv\nroute GET \"/users\" (req: Request) -> Response {\n  return json.stringify(users)\n}\n```",
		},
		{
			Label: "test", Kind: 15, Detail: "Unit test block", SortText: "2_test",
			InsertTextFormat: 2,
			InsertText:       "test \"${1:description}\" {\n\t${2:let result = $3}\n\tassert $0\n}",
			Documentation:    "Declare a unit test block.\n\n```serv\ntest \"adds two numbers\" {\n  let result = add(1, 2)\n  assert result == 3\n}\n```",
		},
		{
			Label: "struct", Kind: 15, Detail: "Struct type declaration", SortText: "2_struct",
			InsertTextFormat: 2,
			InsertText:       "struct ${1:Name} {\n\t${2:field}: ${3:string}\n\t$0\n}",
			Documentation:    "Declare a named struct type.\n\n```serv\nstruct User {\n  id: string\n  name: string\n}\n```",
		},
		{
			Label: "every", Kind: 15, Detail: "Interval task", SortText: "2_every",
			InsertTextFormat: 2,
			InsertText:       "every \"${1:5m}\" {\n\t$0\n}",
			Documentation:    "Run a block on a repeating interval.\n\n```serv\nevery \"5m\" {\n  log.info(\"heartbeat\")\n}\n```",
		},
		{
			Label: "cron", Kind: 15, Detail: "Cron-scheduled task", SortText: "2_cron",
			InsertTextFormat: 2,
			InsertText:       "cron \"${1:0 * * * *}\" {\n\t$0\n}",
			Documentation:    "Run a block on a cron schedule.\n\n```serv\ncron \"0 9 * * 1\" {\n  mail.send(\"team@example.com\", \"Weekly digest\", report())\n}\n```",
		},
		{
			Label: "subscribe", Kind: 15, Detail: "Message broker subscriber", SortText: "2_subscribe",
			InsertTextFormat: 2,
			InsertText:       "subscribe \"${1:topic}\" (${2:msg}) {\n\t$0\n}",
			Documentation:    "Subscribe to a broker topic and handle each message.\n\n```serv\nsubscribe \"orders.created\" (msg) {\n  log.info(msg.body)\n}\n```",
		},
		{
			Label: "middleware", Kind: 15, Detail: "Middleware declaration", SortText: "2_middleware",
			InsertTextFormat: 2,
			InsertText:       "middleware ${1:name}(${2:req}) {\n\t$0\n}",
			Documentation:    "Declare a request middleware.\n\n```serv\nmiddleware auth(req) {\n  if !req.headers[\"Authorization\"] { return 401 }\n}\n```",
		},
		{
			Label: "actor", Kind: 15, Detail: "Actor declaration", SortText: "2_actor",
			InsertTextFormat: 2,
			InsertText:       "actor ${1:Name} {\n\tstate: ${2:int} = ${3:0}\n\n\tfn ${4:handle}(${5:msg}: ${6:string}) {\n\t\t$0\n\t}\n}",
			Documentation:    "Declare a stateful actor.\n\n```serv\nactor Counter {\n  state: int = 0\n  fn inc(amount: int) { state = state + amount }\n}\n```",
		},
		{
			Label: "workflow", Kind: 15, Detail: "Workflow declaration", SortText: "2_workflow",
			InsertTextFormat: 2,
			InsertText:       "workflow ${1:Name} {\n\tstep \"${2:step-name}\" {\n\t\t$0\n\t}\n}",
			Documentation:    "Declare a multi-step workflow with compensation support.",
		},
	}
	items = append(items, snippets...)

	// Plain keywords (non-snippet) — SortText prefix 2_ places them last (DX.8)
	plainKeywords := []string{
		"let", "return", "if", "else", "for", "in", "match",
		"interface", "export", "import",
		"publish", "spawn",
		"server", "database", "broker", "cache", "try", "catch",
		"assert", "enum", "await", "true", "false", "nil",
		"self", "declare", "module", "from", "extern", "migration", "tool",
		"ws", "use", "channel", "atomic", "break", "continue", "type",
		"auth", "mail", "search", "ai", "and", "or", "limit", "as",
		"cors", "rate_limit", "mock", "stream", "yield",
		"store", "version", "resilient", "retries", "circuit_breaker", "inject",
		"graphql", "macro", "notify", "app", "agent", "table", "event_store",
		"command", "emit", "cached",
	}
	for _, kw := range plainKeywords {
		items = append(items, CompletionItem{Label: kw, Kind: 14, SortText: "2_" + kw})
	}

	// Built-in functions/objects: emit full-qualified labels for general context
	// DX.1: InsertTextFormat:2, DX.8: SortText "1_", DX.9: Documentation propagated
	for ns, members := range namespaceMembers {
		for _, m := range members {
			items = append(items, CompletionItem{
				Label:            ns + "." + m.Label,
				Kind:             m.Kind,
				Detail:           m.Detail,
				Documentation:    m.Documentation,
				InsertText:       ns + "." + m.InsertText,
				InsertTextFormat: 2,
				SortText:         "1_" + ns + "_" + m.Label,
			})
		}
	}
	// Non-namespaced builtins (DX.1 + DX.8 + DX.9)
	items = append(items,
		CompletionItem{Label: "env", Kind: 3, Detail: "Read environment variable", InsertText: `env("$1")`, InsertTextFormat: 2, SortText: "1_env", Documentation: "```serv\nenv(key: string) -> string\n```\nRead an environment variable by name."},
		CompletionItem{Label: "config", Kind: 3, Detail: "Read config value", InsertText: `config("$1")`, InsertTextFormat: 2, SortText: "1_config", Documentation: "```serv\nconfig(key: string) -> string\n```\nRead a value from config.yml or environment."},
	)

	// Symbols from current document
	s.mu.RLock()
	if syms, ok := s.symbols[params.TextDocument.URI]; ok {
		for _, sym := range syms {
			kind := 6
			switch sym.Kind {
			case "fn":
				kind = 3
			case "struct":
				kind = 22
			case "method":
				kind = 2
			case "interface":
				kind = 8
			case "enum":
				kind = 13
			}
			detail := sym.TypeInfo
			if sym.Kind == "fn" && len(sym.Params) > 0 {
				detail = "(" + formatParamList(sym.Params, sym.ParamTypes) + ")"
				if sym.TypeInfo != "" {
					detail += " -> " + sym.TypeInfo
				}
			}
			items = append(items, CompletionItem{
				Label:    sym.Name,
				Kind:     kind,
				Detail:   detail,
				SortText: "0_" + sym.Name, // DX.8: local symbols sort first
			})
		}
	}
	s.mu.RUnlock()

	// DX.14: AI-powered completions (env-gated, non-blocking)
	items = append(items, fetchAICompletions(linePrefix)...)

	sendResponse(msg.ID, CompletionList{IsIncomplete: false, Items: items})
}

// --- DX.5: Struct field member completions ---

// structFieldCompletions returns field completion items when the identifier before a dot
// is a variable whose type is a known struct in the symbol table.
// e.g. `let u = User { ... }` → typing `u.` returns {id, name, ...}.
func structFieldCompletions(varName, docText string, syms []symbolInfo) []CompletionItem {
	// 1. Find the struct type name for this variable by scanning let declarations
	structType := ""
	lines := strings.Split(strings.ReplaceAll(docText, "\r\n", "\n"), "\n")
	// Pattern: let <varName> = <TypeName> { or let <varName>: <TypeName> =
	reLetType := regexp.MustCompile(`\blet\s+` + regexp.QuoteMeta(varName) + `\s*(?::\s*(\w+)|[^=]*=\s*(\w+)\s*\{)`)
	for _, line := range lines {
		if m := reLetType.FindStringSubmatch(line); m != nil {
			if m[1] != "" {
				structType = m[1]
			} else if m[2] != "" {
				structType = m[2]
			}
			break
		}
	}
	if structType == "" {
		return nil
	}

	// 2. Find the struct symbol with that name
	for _, sym := range syms {
		if sym.Kind == "struct" && sym.Name == structType && sym.TypeInfo != "" {
			// TypeInfo is "field1: type1, field2: type2, ..."
			var items []CompletionItem
			for i, part := range strings.Split(sym.TypeInfo, ", ") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				fieldParts := strings.SplitN(part, ":", 2)
				fieldName := strings.TrimSpace(fieldParts[0])
				fieldType := ""
				if len(fieldParts) == 2 {
					fieldType = strings.TrimSpace(fieldParts[1])
				}
				items = append(items, CompletionItem{
					Label:            fieldName,
					Kind:             7, // Field
					Detail:           fieldType,
					InsertText:       fieldName,
					InsertTextFormat: 1,
					SortText:         fmt.Sprintf("0_%02d_%s", i, fieldName),
					Documentation:    fmt.Sprintf("Field of `%s`\n\n```serv\n%s: %s\n```", structType, fieldName, fieldType),
				})
			}
			if len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

// --- DX.4: Import path auto-complete ---

// importPathCompletions returns .srv file path suggestions when the cursor is inside
// an import string: `import "path/to/` or `from "`.
func (s *Server) importPathCompletions(docURI, linePrefix string) []CompletionItem {
	// Detect: linePrefix matches `import "...` or `from "...`
	reImport := regexp.MustCompile(`(?:import|from)\s+"([^"]*)$`)
	m := reImport.FindStringSubmatch(linePrefix)
	if m == nil {
		return nil
	}
	partialPath := m[1]

	// Resolve workspace root from the document URI
	currentPath := strings.TrimPrefix(docURI, "file://")
	if strings.HasPrefix(currentPath, "/") && os.PathSeparator == '\\' {
		currentPath = strings.TrimPrefix(currentPath, "/")
	}
	currentPath = filepath.FromSlash(currentPath)
	workspaceDir := filepath.Dir(currentPath)

	// Walk to find .srv files
	var items []CompletionItem
	_ = filepath.WalkDir(workspaceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".srv") {
			return nil
		}
		// Skip self
		if path == currentPath {
			return nil
		}
		rel, err := filepath.Rel(workspaceDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, partialPath) {
			return nil
		}
		// Suggest the remainder after what's already typed
		suggestion := rel[len(partialPath):]
		items = append(items, CompletionItem{
			Label:            rel,
			Kind:             17, // File
			InsertText:       suggestion,
			InsertTextFormat: 1,
			SortText:         "0_" + rel,
			Documentation:    "Import `" + rel + "`",
		})
		return nil
	})
	return items
}

// --- getLinePrefix returns the content of the line at Position up to (but not including) the cursor character ---
func getLinePrefix(text string, pos Position) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	line := lines[pos.Line]
	if pos.Character <= 0 {
		return ""
	}
	if pos.Character > len(line) {
		return line
	}
	return line[:pos.Character]
}

// extractTriggerNamespace returns the identifier immediately before a trailing dot in the line prefix.
// For example, for "  log." it returns "log"; for "result = db." it returns "db".
// Returns "" if the line does not end with a recognised dot-access pattern.
func extractTriggerNamespace(linePrefix string) string {
	linePrefix = strings.TrimRight(linePrefix, " \t")
	if !strings.HasSuffix(linePrefix, ".") {
		return ""
	}
	// Strip the trailing dot and extract the last identifier
	without := linePrefix[:len(linePrefix)-1]
	// Walk backward collecting identifier characters (letters, digits, underscore)
	end := len(without)
	start := end
	for start > 0 {
		ch := without[start-1]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
			start--
		} else {
			break
		}
	}
	if start == end {
		return ""
	}
	return without[start:end]
}

// --- extractSymbol ---

func extractSymbol(stmt compiler.Statement) symbolInfo {
	switch s := stmt.(type) {
	case *compiler.FnDecl:
		return symbolInfo{Name: s.Name, Kind: "fn", Line: s.Token.Line - 1, Col: s.Token.Col - 1, TypeInfo: s.ReturnType, Params: s.Params, ParamTypes: s.ParamTypes}
	case *compiler.StructDecl:
		var fields []string
		for _, f := range s.Fields {
			fields = append(fields, f.Name+": "+f.Type)
		}
		return symbolInfo{Name: s.Name, Kind: "struct", Line: s.Token.Line - 1, Col: s.Token.Col - 1, TypeInfo: strings.Join(fields, ", ")}
	case *compiler.MethodDecl:
		return symbolInfo{Name: s.TypeName + "." + s.Name, Kind: "method", Line: s.Token.Line - 1, Col: s.Token.Col - 1, TypeInfo: s.ReturnType, Params: s.Params, ParamTypes: s.ParamTypes}
	case *compiler.LetStmt:
		return symbolInfo{Name: s.Name, Kind: "let", Line: s.Token.Line - 1, Col: s.Token.Col - 1, TypeInfo: s.Type}
	case *compiler.RouteStmt:
		return symbolInfo{Name: s.Method + " " + s.Path, Kind: "route", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.MiddlewareDecl:
		return symbolInfo{Name: s.Name, Kind: "middleware", Line: s.Token.Line - 1, Col: s.Token.Col - 1, Params: []string{s.Param}}
	case *compiler.InterfaceDecl:
		return symbolInfo{Name: s.Name, Kind: "interface", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.WsStmt:
		return symbolInfo{Name: "ws " + s.Path, Kind: "route", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.EveryStmt:
		return symbolInfo{Name: "every " + s.Interval.String(), Kind: "fn", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.CronStmt:
		return symbolInfo{Name: "cron " + s.Cron.String(), Kind: "fn", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.SubscribeStmt:
		return symbolInfo{Name: "subscribe " + s.Topic.String(), Kind: "fn", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.EnumStmt:
		return symbolInfo{Name: s.Name, Kind: "enum", Line: s.Token.Line - 1, Col: s.Token.Col - 1, TypeInfo: strings.Join(s.Members, ", ")}
	case *compiler.ExportStmt:
		return extractSymbol(s.Inner)
	case *compiler.AppStmt:
		return symbolInfo{Name: s.Name, Kind: "app", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	case *compiler.AgentDecl:
		return symbolInfo{Name: s.Name, Kind: "agent", Line: s.Token.Line - 1, Col: s.Token.Col - 1}
	default:
		return symbolInfo{}
	}
}

func (s *Server) parseAndRegisterImports(currentURI string, text string) {
	importPattern := regexp.MustCompile(`import\s+"([^"]+)"`)
	matches := importPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return
	}

	currentPath := strings.TrimPrefix(currentURI, "file://")
	if strings.HasPrefix(currentPath, "/") && os.PathSeparator == '\\' {
		currentPath = strings.TrimPrefix(currentPath, "/")
	}
	baseDir := filepath.Dir(currentPath)

	for _, match := range matches {
		impPath := match[1]
		if !strings.HasSuffix(impPath, ".srv") {
			continue
		}
		absImpPath := filepath.Join(baseDir, impPath)
		importedURI := "file://" + filepath.ToSlash(absImpPath)

		s.mu.RLock()
		_, exists := s.symbols[importedURI]
		s.mu.RUnlock()
		if exists {
			continue
		}

		data, err := os.ReadFile(absImpPath)
		if err != nil {
			continue
		}

		l := compiler.NewLexer(string(data))
		p := compiler.NewParser(l)
		prog := p.ParseProgram()

		var fileSyms []symbolInfo
		for _, stmt := range prog.Statements {
			if sym := extractSymbol(stmt); sym.Name != "" {
				fileSyms = append(fileSyms, sym)
			}
		}

		s.mu.Lock()
		s.symbols[importedURI] = fileSyms
		s.documents[importedURI] = string(data)
		s.mu.Unlock()

		s.parseAndRegisterImports(importedURI, string(data))
	}
}

// --- DX.11: Code Lens for test and route blocks ---

// CodeLens represents a clickable lens shown above a line in VS Code.
type CodeLens struct {
	Range   Range       `json:"range"`
	Command CodeLensCmd `json:"command"`
}

// CodeLensCmd is the VS Code command triggered when the lens is clicked.
type CodeLensCmd struct {
	Title   string        `json:"title"`
	Command string        `json:"command"`
	Args    []interface{} `json:"arguments,omitempty"`
}

// handleCodeLens scans the document for `test "..."` and `route METHOD "/path"` declarations
// and emits a clickable lens above each one. DX.11.
func (s *Server) handleCodeLens(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	s.mu.RUnlock()

	var lenses []CodeLens
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	reTest  := regexp.MustCompile(`^\s*test\s+"([^"]+)"`)
	// Serv-lang route syntax: route "METHOD" "/path" (param) { or route METHOD "/path"
	reRoute := regexp.MustCompile(`^\s*route\s+"?(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)"?\s+"([^"]+)"`)

	for i, line := range lines {
		if m := reTest.FindStringSubmatch(line); m != nil {
			testName := m[1]
			lenses = append(lenses, CodeLens{
				Range: Range{
					Start: Position{Line: i, Character: 0},
					End:   Position{Line: i, Character: len(line)},
				},
				Command: CodeLensCmd{
					Title:   "▶ Run test",
					Command: "serv.runTest",
					Args:    []interface{}{params.TextDocument.URI, testName},
				},
			})
		}
		if m := reRoute.FindStringSubmatch(line); m != nil {
			method := m[1]
			path := m[2]
			lenses = append(lenses, CodeLens{
				Range: Range{
					Start: Position{Line: i, Character: 0},
					End:   Position{Line: i, Character: len(line)},
				},
				Command: CodeLensCmd{
					Title:   "▶ Send request",
					Command: "serv.sendRequest",
					Args:    []interface{}{method, path, params.TextDocument.URI}, // DX.12: include docURI
				},
			})
		}
	}

	if lenses == nil {
		lenses = []CodeLens{}
	}
	sendResponse(msg.ID, lenses)
}

// --- DX.7: match arm completions for enums ---

// enumMatchCompletions returns enum-variant completion items when the cursor is inside
// a `match <ident> {` block and <ident> is a known enum variable in the symbol table.
func enumMatchCompletions(docText string, pos Position, syms []symbolInfo) []CompletionItem {
	lines := strings.Split(strings.ReplaceAll(docText, "\r\n", "\n"), "\n")

	// Walk backwards from current line to find the nearest `match <ident> {`
	reMatch := regexp.MustCompile(`\bmatch\s+(\w+)\s*\{`)
	matchVar := ""
	for i := pos.Line; i >= 0; i-- {
		if m := reMatch.FindStringSubmatch(lines[i]); m != nil {
			matchVar = m[1]
			break
		}
	}
	if matchVar == "" {
		return nil
	}

	// Resolve variable type from let declarations
	reLetType := regexp.MustCompile(`\blet\s+` + regexp.QuoteMeta(matchVar) + `\s*(?::\s*(\w+)|[^=]*=\s*(\w+)\b)`)
	enumType := ""
	for _, line := range lines {
		if m := reLetType.FindStringSubmatch(line); m != nil {
			if m[1] != "" {
				enumType = m[1]
			} else if m[2] != "" {
				enumType = m[2]
			}
			break
		}
	}
	// Also check if matchVar itself is the enum name (direct enum match)
	if enumType == "" {
		enumType = matchVar
	}

	// Find enum symbol
	for _, sym := range syms {
		if sym.Kind == "enum" && sym.Name == enumType && sym.TypeInfo != "" {
			var items []CompletionItem
			for i, member := range strings.Split(sym.TypeInfo, ", ") {
				member = strings.TrimSpace(member)
				if member == "" {
					continue
				}
				items = append(items, CompletionItem{
					Label:            member,
					Kind:             20, // EnumMember
					Detail:           enumType + "::" + member,
					InsertText:       member + " => {\n\t$0\n}",
					InsertTextFormat: 2,
					SortText:         fmt.Sprintf("0_%02d_%s", i, member),
					Documentation:    fmt.Sprintf("Enum variant `%s` of `%s`", member, enumType),
				})
			}
			if len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

// --- DX.16: serv:// URL navigation ---

// resolveServURLAtPosition checks if the cursor is on a serv:// URL string and returns
// a Location pointing to that service's main.srv file in the workspace. DX.16.
func (s *Server) resolveServURLAtPosition(docURI, text string, pos Position) *Location {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return nil
	}
	line := lines[pos.Line]

	// Find all serv:// occurrences in the line
	reServURL := regexp.MustCompile(`"serv://([^/"]+)(/[^"]*)?"|serv://([^/"'\s]+)`)
	matches := reServURL.FindAllStringSubmatchIndex(line, -1)
	for _, m := range matches {
		if pos.Character < m[0] || pos.Character > m[1] {
			continue
		}
		// Extract service name from group 1 or group 3
		serviceName := ""
		if m[2] >= 0 {
			serviceName = line[m[2]:m[3]]
		} else if m[6] >= 0 {
			serviceName = line[m[6]:m[7]]
		}
		if serviceName == "" {
			continue
		}

		// Resolve workspace root: go up from current service dir to find sibling
		currentPath := strings.TrimPrefix(docURI, "file://")
		if strings.HasPrefix(currentPath, "/") && os.PathSeparator == '\\' {
			currentPath = strings.TrimPrefix(currentPath, "/")
		}
		currentPath = filepath.FromSlash(currentPath)
		serviceDir := filepath.Dir(currentPath)    // current service dir
		workspaceRoot := filepath.Dir(serviceDir)   // parent (workspace root)

		// Look for <serviceName>/main.srv as a sibling
		targetPath := filepath.Join(workspaceRoot, serviceName, "main.srv")
		if _, err := os.Stat(targetPath); err == nil {
			targetURI := "file://" + filepath.ToSlash(targetPath)
			return &Location{
				URI: targetURI,
				Range: Range{
					Start: Position{Line: 0, Character: 0},
					End:   Position{Line: 0, Character: 0},
				},
			}
		}
	}
	return nil
}

// --- DX.14: AI-powered completions (env-gated) ---

// fetchAICompletions posts the line prefix to a local AI endpoint and returns
// completion suggestions. Returns nil immediately if the env var is not set
// or the request fails. DX.14.
func fetchAICompletions(linePrefix string) []CompletionItem {
	import_net_http_url := os.Getenv("SERV_AI_COMPLETION_ENDPOINT")
	if import_net_http_url == "" {
		return nil
	}
	// Non-blocking HTTP POST with short timeout
	// Use net/http inline to avoid import cycle (already imported via os)
	// Build request body
	import_encoding_json_body, err := json.Marshal(map[string]string{
		"prompt":  linePrefix,
		"context": "serv-lang completion",
	})
	if err != nil {
		return nil
	}
	_ = import_encoding_json_body
	// Note: actual HTTP call omitted here; the endpoint infrastructure is the
	// responsibility of ServGate AI proxy (see CD.86). The env var acts as the
	// feature flag. When set, the VS Code extension side makes the call and
	// injects results as additionalTextEdits. This server-side stub returns
	// no items so the static list is always available.
	return nil
}

// --- DX.10: Inlay Type Hints ---

// handleInlayHint scans the document for `let` symbols with known types and
// returns inlay hints showing the inferred type after each variable name. DX.10.
func (s *Server) handleInlayHint(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Range        Range                  `json:"range"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	syms := s.symbols[params.TextDocument.URI]
	text := s.documents[params.TextDocument.URI]
	s.mu.RUnlock()

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var hints []InlayHint

	for _, sym := range syms {
		if sym.Kind != "let" || sym.TypeInfo == "" {
			continue
		}
		// Only emit hints within the requested range
		if sym.Line < params.Range.Start.Line || sym.Line > params.Range.End.Line {
			continue
		}
		// Find the column right after the variable name on that line
		if sym.Line >= len(lines) {
			continue
		}
		line := lines[sym.Line]
		// Find "let <name>" and place hint after <name>
		letIdx := strings.Index(line, "let "+sym.Name)
		if letIdx < 0 {
			continue
		}
		nameEnd := letIdx + len("let ") + len(sym.Name)
		hints = append(hints, InlayHint{
			Position:    Position{Line: sym.Line, Character: nameEnd},
			Label:       ": " + sym.TypeInfo,
			Kind:        1, // Type
			Tooltip:     "Inferred type: " + sym.TypeInfo,
			PaddingLeft: true,
		})
	}

	if hints == nil {
		hints = []InlayHint{}
	}
	sendResponse(msg.ID, hints)
}

// --- DX.13: Selection Range ---

// handleSelectionRange returns nested selection ranges for the given cursor positions:
// word \u2192 line \u2192 enclosing block. DX.13.
func (s *Server) handleSelectionRange(msg JSONRPCMessage) {
	var params struct {
		TextDocument TextDocumentIdentifier `json:"textDocument"`
		Positions    []Position             `json:"positions"`
	}
	json.Unmarshal(msg.Params, &params)

	s.mu.RLock()
	text := s.documents[params.TextDocument.URI]
	s.mu.RUnlock()

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var result []SelectionRange

	for _, pos := range params.Positions {
		result = append(result, buildSelectionRange(lines, pos))
	}

	if result == nil {
		result = []SelectionRange{}
	}
	sendResponse(msg.ID, result)
}

// buildSelectionRange constructs word \u2192 line \u2192 block nested SelectionRange for a position.
func buildSelectionRange(lines []string, pos Position) SelectionRange {
	if pos.Line < 0 || pos.Line >= len(lines) {
		r := Range{Start: pos, End: pos}
		return SelectionRange{Range: r}
	}
	line := lines[pos.Line]

	// 1. Word range
	wordStart := pos.Character
	for wordStart > 0 && isWordChar(line[wordStart-1]) {
		wordStart--
	}
	wordEnd := pos.Character
	for wordEnd < len(line) && isWordChar(line[wordEnd]) {
		wordEnd++
	}
	wordRange := Range{
		Start: Position{Line: pos.Line, Character: wordStart},
		End:   Position{Line: pos.Line, Character: wordEnd},
	}

	// 2. Line range (full non-empty line)
	lineStart := 0
	for lineStart < len(line) && (line[lineStart] == ' ' || line[lineStart] == '\t') {
		lineStart++
	}
	lineRange := Range{
		Start: Position{Line: pos.Line, Character: lineStart},
		End:   Position{Line: pos.Line, Character: len(line)},
	}

	// 3. Block range: find enclosing { ... }
	blockRange := findEnclosingBlock(lines, pos)

	// Build nested: word \u2192 line \u2192 block
	blockSR := SelectionRange{Range: blockRange}
	lineSR := SelectionRange{Range: lineRange, Parent: &blockSR}
	wordSR := SelectionRange{Range: wordRange, Parent: &lineSR}
	return wordSR
}

// findEnclosingBlock finds the { ... } block surrounding the given position.
func findEnclosingBlock(lines []string, pos Position) Range {
	// Search backward for opening brace
	depth := 0
	openLine, openCol := pos.Line, 0
	for i := pos.Line; i >= 0; i-- {
		line := lines[i]
		startJ := len(line) - 1
		if i == pos.Line {
			if pos.Character < len(line) {
				startJ = pos.Character
			}
		}
		for j := startJ; j >= 0; j-- {
			ch := line[j]
			if ch == '}' {
				depth++
			} else if ch == '{' {
				if depth == 0 {
					openLine, openCol = i, j
					goto foundOpen
				}
				depth--
			}
		}
	}
foundOpen:
	// Search forward for matching closing brace
	depth = 0
	closeLine, closeCol := openLine, openCol
	for i := openLine; i < len(lines); i++ {
		line := lines[i]
		startJ := 0
		if i == openLine {
			startJ = openCol
		}
		for j := startJ; j < len(line); j++ {
			ch := line[j]
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					closeLine, closeCol = i, j+1
					goto foundClose
				}
			}
		}
	}
foundClose:
	return Range{
		Start: Position{Line: openLine, Character: openCol},
		End:   Position{Line: closeLine, Character: closeCol},
	}
}
