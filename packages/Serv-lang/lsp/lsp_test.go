package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Helper to capture stdout and call handler
func captureStdout(fn func()) string {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	outChan := make(chan string)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		outChan <- buf.String()
	}()

	fn()
	w.Close()
	os.Stdout = oldStdout
	return <-outChan
}

func parseLSPResponse(t *testing.T, output string) JSONRPCMessage {
	// Format is: Content-Length: <n>\r\n\r\n<body>
	parts := strings.SplitN(output, "\r\n\r\n", 2)
	if len(parts) < 2 {
		t.Fatalf("Invalid LSP response format: %q", output)
	}
	body := parts[1]
	var msg JSONRPCMessage
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("Failed to parse response JSON: %v. Body was: %s", err, body)
	}
	return msg
}

func TestPrepareRename(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	content := `fn my_function() {
	let local_var = 10
	log.info("hello")
}`
	server.documents[uri] = content

	// PrepareRename on "my_function" (line 0, col 5)
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "textDocument/prepareRename",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":0,"character":5}}`),
	}

	out := captureStdout(func() {
		server.handleMessage(msg)
	})

	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Errorf("Expected no error, got: %v", resp.Error)
	}

	var r Range
	resBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	err = json.Unmarshal(resBytes, &r)
	if err != nil {
		t.Fatalf("Failed to unmarshal result range: %v. Result was: %s", err, string(resBytes))
	}

	if r.Start.Line != 0 || r.Start.Character != 3 || r.End.Line != 0 || r.End.Character != 14 {
		t.Errorf("Expected range for 'my_function' (0:3 to 0:14), got start=(%d:%d) end=(%d:%d)",
			r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
	}

	// PrepareRename on "log" (built-in, line 2, col 1) -> should reject
	msg2 := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "textDocument/prepareRename",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":2,"character":1}}`),
	}

	out2 := captureStdout(func() {
		server.handleMessage(msg2)
	})

	resp2 := parseLSPResponse(t, out2)
	// LSP spec says prepareRename returns null if rename is not valid at the position
	if resp2.Result != nil {
		res2Bytes, _ := json.Marshal(resp2.Result)
		if string(res2Bytes) != "null" {
			t.Errorf("Expected result to be null for keyword/built-in, got: %s", string(res2Bytes))
		}
	}
}

func TestReferencesAndRenameLocalScope(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	content := `fn my_func() {
	let val = 1
	val = val + 1
}

fn other_func() {
	let val = 2
}`
	server.documents[uri] = content

	// Add symbols to trigger local variable scope lookup
	server.symbols[uri] = []symbolInfo{
		{Name: "my_func", Kind: "fn", Line: 0, Col: 3},
		{Name: "other_func", Kind: "fn", Line: 5, Col: 3},
	}

	// References for "val" in my_func (line 1, col 5)
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "textDocument/references",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":1,"character":5}}`),
	}

	out := captureStdout(func() {
		server.handleMessage(msg)
	})

	resp := parseLSPResponse(t, out)
	var locations []Location
	resBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	if err := json.Unmarshal(resBytes, &locations); err != nil {
		t.Fatalf("Failed to parse locations: %v", err)
	}

	// Should only find occurrences of "val" inside my_func (lines 1, 2)
	// And NOT inside other_func (line 6)
	if len(locations) != 3 {
		t.Fatalf("Expected 3 locations of 'val' in my_func, got %d: %+v", len(locations), locations)
	}

	for _, loc := range locations {
		if loc.Range.Start.Line > 2 {
			t.Errorf("Found reference outside local scope at line %d", loc.Range.Start.Line)
		}
	}

	// Rename "val" in my_func (line 1, col 5)
	msgRename := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "textDocument/rename",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":1,"character":5},"newName":"newVal"}`),
	}

	outRename := captureStdout(func() {
		server.handleMessage(msgRename)
	})

	respRename := parseLSPResponse(t, outRename)
	var edit WorkspaceEdit
	resRenameBytes, err := json.Marshal(respRename.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	if err := json.Unmarshal(resRenameBytes, &edit); err != nil {
		t.Fatalf("Failed to parse workspace edit: %v", err)
	}

	edits := edit.Changes[uri]
	if len(edits) != 3 {
		t.Errorf("Expected 3 edits, got %d", len(edits))
	}
	for _, e := range edits {
		if e.NewText != "newVal" {
			t.Errorf("Expected new name 'newVal', got '%s'", e.NewText)
		}
	}
}

func TestReferencesAndRenameGlobalScope(t *testing.T) {
	server := NewServer()
	uri1 := "file:///file1.srv"
	uri2 := "file:///file2.srv"
	server.documents[uri1] = `fn global_helper() {
	return 42
}`
	server.documents[uri2] = `import "file1.srv"
fn main() {
	let x = global_helper()
}`
	// Add global symbols
	server.symbols[uri1] = []symbolInfo{
		{Name: "global_helper", Kind: "fn", Line: 0, Col: 3},
	}
	server.symbols[uri2] = []symbolInfo{
		{Name: "main", Kind: "fn", Line: 1, Col: 3},
	}

	// References on "global_helper" (line 0, col 3 in file1.srv)
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "textDocument/references",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///file1.srv"},"position":{"line":0,"character":3}}`),
	}

	out := captureStdout(func() {
		server.handleMessage(msg)
	})

	resp := parseLSPResponse(t, out)
	var locations []Location
	resBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	if err := json.Unmarshal(resBytes, &locations); err != nil {
		t.Fatalf("Failed to parse locations: %v", err)
	}

	// Should find global_helper definition in file1.srv and usage in file2.srv
	if len(locations) != 2 {
		t.Fatalf("Expected 2 locations of 'global_helper', got %d: %+v", len(locations), locations)
	}

	hasFile1 := false
	hasFile2 := false
	for _, loc := range locations {
		if loc.URI == uri1 && loc.Range.Start.Line == 0 {
			hasFile1 = true
		}
		if loc.URI == uri2 && loc.Range.Start.Line == 2 {
			hasFile2 = true
		}
	}
	if !hasFile1 || !hasFile2 {
		t.Errorf("Expected reference in both files, got: %+v", locations)
	}

	// Rename "global_helper"
	msgRename := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "textDocument/rename",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///file1.srv"},"position":{"line":0,"character":3},"newName":"helper_new"}`),
	}

	outRename := captureStdout(func() {
		server.handleMessage(msgRename)
	})

	respRename := parseLSPResponse(t, outRename)
	var edit WorkspaceEdit
	resRenameBytes, err := json.Marshal(respRename.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	if err := json.Unmarshal(resRenameBytes, &edit); err != nil {
		t.Fatalf("Failed to parse workspace edit: %v", err)
	}

	if len(edit.Changes[uri1]) != 1 || len(edit.Changes[uri2]) != 1 {
		t.Errorf("Expected edits in both file1 and file2, got: %+v", edit.Changes)
	}
}

func TestCompletion(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	server.documents[uri] = "let x = 10"

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "textDocument/completion",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":0,"character":5}}`),
	}

	out := captureStdout(func() {
		server.handleMessage(msg)
	})

	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("Expected no error, got: %v", resp.Error)
	}

	var list CompletionList
	resBytes, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("Failed to marshal result: %v", err)
	}
	if err := json.Unmarshal(resBytes, &list); err != nil {
		t.Fatalf("Failed to parse completion list: %v. Body was: %s", err, string(resBytes))
	}

	foundKw := false
	foundBuiltin := false
	for _, item := range list.Items {
		if item.Label == "fn" {
			foundKw = true
		}
		if item.Label == "log.info" {
			foundBuiltin = true
		}
	}
	if !foundKw {
		t.Errorf("Expected keywords (like 'fn') in completion list")
	}
	if !foundBuiltin {
		t.Errorf("Expected builtins (like 'log.info') in completion list")
	}
}

func TestLSPCompletionContextAware(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"

	tests := []struct {
		name           string
		content        string
		charPos        int
		wantLabels     []string
		notWantLabels  []string
	}{
		{
			name:          "log dot triggers log-only members",
			content:       "  log.",
			charPos:       6,
			wantLabels:    []string{"info", "warn", "error", "debug"},
			notWantLabels: []string{"fn", "log.info", "db.query", "get"},
		},
		{
			name:          "db dot triggers db-only members",
			content:       "let result = db.",
			charPos:       16,
			wantLabels:    []string{"query", "findOne", "insert", "update"},
			notWantLabels: []string{"fn", "log.info", "info"},
		},
		{
			name:          "cache dot triggers cache-only members",
			content:       "  cache.",
			charPos:       8,
			wantLabels:    []string{"get", "set", "delete", "flush"},
			notWantLabels: []string{"fn", "log.info", "query"},
		},
		{
			name:          "unknown namespace falls back to full list",
			content:       "  myObj.",
			charPos:       8,
			wantLabels:    []string{"fn", "log.info"},
			notWantLabels: []string{},
		},
		{
			name:          "no dot returns full list",
			content:       "let x = lo",
			charPos:       10,
			wantLabels:    []string{"fn", "log.info"},
			notWantLabels: []string{},
		},
		{
			name:          "http dot triggers http-only members",
			content:       "http.",
			charPos:       5,
			wantLabels:    []string{"get", "post", "put", "delete", "patch", "static"},
			notWantLabels: []string{"fn", "log.info", "query"},
		},
		{
			name:          "exec dot triggers exec-only members",
			content:       "exec.",
			charPos:       5,
			wantLabels:    []string{"run"},
			notWantLabels: []string{"fn", "log.info", "get"},
		},
		{
			name:          "diff dot triggers diff-only members",
			content:       "diff.",
			charPos:       5,
			wantLabels:    []string{"text", "json"},
			notWantLabels: []string{"fn", "log.info", "get"},
		},
		{
			name:          "proto dot triggers proto-only members",
			content:       "proto.",
			charPos:       6,
			wantLabels:    []string{"encode", "decode"},
			notWantLabels: []string{"fn", "log.info", "get"},
		},
		{
			name:          "encoding.base64 dot triggers base64 members",
			content:       "encoding.base64.",
			charPos:       16,
			wantLabels:    []string{"encode", "decode"},
			notWantLabels: []string{"fn", "log.info", "get"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server.documents[uri] = tc.content

			msg := JSONRPCMessage{
				JSONRPC: "2.0",
				ID:      1,
				Method:  "textDocument/completion",
				Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":0,"character":` + string(rune('0'+tc.charPos/10)) + string(rune('0'+tc.charPos%10)) + `}}`),
			}
			// Use a proper int serialization
			paramsJSON, _ := json.Marshal(map[string]interface{}{
				"textDocument": map[string]string{"uri": uri},
				"position":     map[string]int{"line": 0, "character": tc.charPos},
			})
			msg.Params = json.RawMessage(paramsJSON)

			out := captureStdout(func() {
				server.handleMessage(msg)
			})
			resp := parseLSPResponse(t, out)
			if resp.Error != nil {
				t.Fatalf("Expected no error, got: %v", resp.Error)
			}
			var list CompletionList
			resBytes, _ := json.Marshal(resp.Result)
			if err := json.Unmarshal(resBytes, &list); err != nil {
				t.Fatalf("Failed to parse completion list: %v", err)
			}

			labelSet := map[string]bool{}
			for _, item := range list.Items {
				labelSet[item.Label] = true
			}

			for _, want := range tc.wantLabels {
				if !labelSet[want] {
					t.Errorf("Expected label %q in completion list, got labels: %v", want, labelSet)
				}
			}
			for _, notWant := range tc.notWantLabels {
				if labelSet[notWant] {
					t.Errorf("Did NOT expect label %q in completion list", notWant)
				}
			}
		})
	}
}

func TestMemberHoverDX6(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	server.documents[uri] = "log.info(\"hello\")"

	// Cursor on "info" (character 4-7)
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": 0, "character": 5},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 1, Method: "textDocument/hover", Params: json.RawMessage(paramsJSON)}

	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("Expected no error, got: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Fatal("Expected hover result for log.info, got nil")
	}
	// The result should contain markdown with log.info signature
	resBytes, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(resBytes), "log.info") {
		t.Errorf("Expected hover to mention 'log.info', got: %s", string(resBytes))
	}
}

func TestStructFieldCompletionsDX5(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	// Document declares a struct and a variable of that type, then accesses it
	server.documents[uri] = "struct User {\n  id: string\n  name: string\n}\nlet u = User {\n  id: \"1\"\n  name: \"Alice\"\n}\nlet x = u."
	// Trigger completion after "u."
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": 8, "character": 10},
	})
	// Register symbols (simulate didOpen)
	openMsg := JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  "textDocument/didOpen",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv","languageId":"github.com/vyuvaraj/serv/packages/Serv-lang","version":1,"text":"struct User {\n  id: string\n  name: string\n}\nlet u = User {\n  id: \"1\"\n  name: \"Alice\"\n}\nlet x = u."}}`),
	}
	captureStdout(func() { server.handleMessage(openMsg) })

	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 2, Method: "textDocument/completion", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("Expected no error, got: %v", resp.Error)
	}
	var list CompletionList
	resBytes, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(resBytes, &list); err != nil {
		t.Fatalf("Failed to parse completion list: %v", err)
	}
	labelSet := map[string]bool{}
	for _, item := range list.Items {
		labelSet[item.Label] = true
	}
	for _, want := range []string{"id", "name"} {
		if !labelSet[want] {
			t.Errorf("Expected struct field %q in completions, got: %v", want, labelSet)
		}
	}
}

func TestImportPathCompletionsDX4(t *testing.T) {
	server := NewServer()
	// Use a real URI pointing to the lsp test directory — the file scan needs a real dir
	uri := "file:///f:/Don/servverse/Serv-lang/lsp/test_main.srv"
	server.documents[uri] = `import "`
	// Position right after the opening quote (character 8)
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": 0, "character": 8},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 3, Method: "textDocument/completion", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("Expected no error, got: %v", resp.Error)
	}
	// Result could be an empty list (no .srv files in that dir) or a list — just assert no crash
	if resp.Result == nil {
		t.Fatal("Expected a result (even empty list) for import completion")
	}
}

func TestCodeLensDX11(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	server.documents[uri] = `route GET "/users" (req: Request) -> Response {
  return json.stringify(users)
}

test "fetches users" {
  assert true
}
`
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 4, Method: "textDocument/codeLens", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("Expected no error, got: %v", resp.Error)
	}
	resBytes, _ := json.Marshal(resp.Result)
	body := string(resBytes)

	if !strings.Contains(body, "Run test") {
		t.Errorf("Expected '▶ Run test' lens in result, got: %s", body)
	}
	if !strings.Contains(body, "Send request") {
		t.Errorf("Expected '▶ Send request' lens in result, got: %s", body)
	}
	if !strings.Contains(body, "serv.runTest") {
		t.Errorf("Expected 'serv.runTest' command in result, got: %s", body)
	}
	if !strings.Contains(body, "serv.sendRequest") {
		t.Errorf("Expected 'serv.sendRequest' command in result, got: %s", body)
	}
}

func TestEnumMatchCompletionsDX7(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	doc := `enum Status { Active, Suspended, Inactive }
let s: Status = Status::Active
match s {
  `
	server.documents[uri] = doc
	server.symbols[uri] = []symbolInfo{
		{Name: "Status", Kind: "enum", TypeInfo: "Active, Suspended, Inactive"},
		{Name: "s", Kind: "let", TypeInfo: "Status"},
	}
	// Cursor at line 3 (inside match block)
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": 3, "character": 2},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 10, Method: "textDocument/completion", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("DX.7: unexpected error: %v", resp.Error)
	}
	var list CompletionList
	resBytes, _ := json.Marshal(resp.Result)
	json.Unmarshal(resBytes, &list)
	labelSet := map[string]bool{}
	for _, item := range list.Items {
		labelSet[item.Label] = true
	}
	for _, want := range []string{"Active", "Suspended", "Inactive"} {
		if !labelSet[want] {
			t.Errorf("DX.7: expected enum member %q in completions, got: %v", want, labelSet)
		}
	}
}

func TestCodeLensDX12RouteArgs(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	// Serv-lang route syntax: route "METHOD" "/path" (param) { ... }
	server.documents[uri] = "route \"GET\" \"/users\" (req) {\n  return 1\n}\n"
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 11, Method: "textDocument/codeLens", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	resBytes, _ := json.Marshal(resp.Result)
	body := string(resBytes)
	// DX.12: Args must include the docURI as 3rd element
	if !strings.Contains(body, "file:///test.srv") {
		t.Errorf("DX.12: expected docURI in route lens Args, got: %s", body)
	}
}

func TestRouteReturnLintingDX15(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	// Route with NO return — should generate a warning diagnostic
	// Serv-lang route syntax: route "METHOD" "/path" (param) { ... }
	noReturnRoute := "route \"GET\" \"/empty\" (req) {\n  let x = 1\n}\n"
	out := captureStdout(func() {
		server.analyzeAndPublishDiagnostics(uri, noReturnRoute)
	})
	resp := parseLSPResponse(t, out)
	var params struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	resBytes, _ := json.Marshal(resp.Params)
	json.Unmarshal(resBytes, &params)

	found := false
	for _, d := range params.Diagnostics {
		if strings.Contains(d.Message, "no return statement") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DX.15: expected 'no return statement' warning for route with no return, got: %+v", params.Diagnostics)
	}

	// Route WITH return — should NOT generate the warning
	uri2 := "file:///test2.srv"
	withReturnRoute := "route \"GET\" \"/ok\" (req) {\n  return 1\n}\n"
	out2 := captureStdout(func() {
		server.analyzeAndPublishDiagnostics(uri2, withReturnRoute)
	})
	resp2 := parseLSPResponse(t, out2)
	var params2 struct {
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	resBytes2, _ := json.Marshal(resp2.Params)
	json.Unmarshal(resBytes2, &params2)
	for _, d := range params2.Diagnostics {
		if strings.Contains(d.Message, "no return statement") {
			t.Errorf("DX.15: unexpected 'no return statement' warning for route with return: %s", d.Message)
		}
	}
}

func TestServURLNavigationDX16(t *testing.T) {
	server := NewServer()
	// Use a real temp path structure: workspace/svc-a/main.srv -> serv://svc-b
	// We can't guarantee svc-b/main.srv exists on disk, so just verify no crash + nil
	uri := "file:///f:/Don/servverse/Serv-lang/main.srv"
	server.documents[uri] = `let url = "serv://auth/login"`
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": 0, "character": 14},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 12, Method: "textDocument/definition", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	// Should return nil (sibling dir doesn't exist in test env) or a valid Location — no panic
	if resp.Error != nil {
		t.Errorf("DX.16: unexpected error: %v", resp.Error)
	}
}

func TestInlayHintsDX10(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	server.documents[uri] = "let count: int = 42\nlet name: string = \"Alice\"\n"
	server.symbols[uri] = []symbolInfo{
		{Name: "count", Kind: "let", Line: 0, Col: 4, TypeInfo: "int"},
		{Name: "name", Kind: "let", Line: 1, Col: 4, TypeInfo: "string"},
	}
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"range": map[string]interface{}{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": 10, "character": 0},
		},
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 13, Method: "textDocument/inlayHint", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("DX.10: unexpected error: %v", resp.Error)
	}
	resBytes, _ := json.Marshal(resp.Result)
	body := string(resBytes)
	if !strings.Contains(body, ": int") {
		t.Errorf("DX.10: expected inlay hint ': int' for count, got: %s", body)
	}
	if !strings.Contains(body, ": string") {
		t.Errorf("DX.10: expected inlay hint ': string' for name, got: %s", body)
	}
}

func TestSelectionRangeDX13(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	server.documents[uri] = "fn greet(name: string) -> string {\n  return \"Hello\"\n}"
	paramsJSON, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": uri},
		"positions":    []map[string]int{{"line": 0, "character": 3}}, // cursor on "greet"
	})
	msg := JSONRPCMessage{JSONRPC: "2.0", ID: 14, Method: "textDocument/selectionRange", Params: json.RawMessage(paramsJSON)}
	out := captureStdout(func() { server.handleMessage(msg) })
	resp := parseLSPResponse(t, out)
	if resp.Error != nil {
		t.Fatalf("DX.13: unexpected error: %v", resp.Error)
	}
	resBytes, _ := json.Marshal(resp.Result)
	body := string(resBytes)
	// Should have nested range with parent
	if !strings.Contains(body, "parent") {
		t.Errorf("DX.13: expected nested selection ranges with 'parent' field, got: %s", body)
	}
	// Word range should cover "fn" at character 0-2
	if !strings.Contains(body, `"character":0`) {
		t.Errorf("DX.13: expected word range start at character 0, got: %s", body)
	}
}

func TestAICompletionDisabledDX14(t *testing.T) {
	// DX.14: When env var is not set, fetchAICompletions must return nil (no panic, no HTTP call)
	items := fetchAICompletions("log.")
	if items != nil {
		t.Errorf("DX.14: expected nil when SERV_AI_COMPLETION_ENDPOINT not set, got %v", items)
	}
}

func TestHover(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	content := `fn my_helper() -> string {
	return "test"
}`
	server.documents[uri] = content
	server.symbols[uri] = []symbolInfo{
		{Name: "my_helper", Kind: "fn", Line: 0, Col: 3, Params: []string{}, ParamTypes: []string{}, TypeInfo: "string"},
	}

	// 1. Test custom function hover
	msg1 := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "textDocument/hover",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":0,"character":6}}`),
	}

	out1 := captureStdout(func() {
		server.handleMessage(msg1)
	})

	resp1 := parseLSPResponse(t, out1)
	var h1 Hover
	res1Bytes, _ := json.Marshal(resp1.Result)
	json.Unmarshal(res1Bytes, &h1)

	if !strings.Contains(h1.Contents.Value, "fn my_helper() -> string") {
		t.Errorf("Expected hover content to describe my_helper, got: %q", h1.Contents.Value)
	}

	// 2. Test built-in object hover (e.g. log)
	server.documents[uri] = "log.info(\"hello\")"
	msg2 := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "textDocument/hover",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":0,"character":1}}`),
	}

	out2 := captureStdout(func() {
		server.handleMessage(msg2)
	})

	resp2 := parseLSPResponse(t, out2)
	var h2 Hover
	res2Bytes, _ := json.Marshal(resp2.Result)
	json.Unmarshal(res2Bytes, &h2)

	if !strings.Contains(h2.Contents.Value, "logger") {
		t.Errorf("Expected hover to describe log built-in, got: %q", h2.Contents.Value)
	}
}

func TestDefinition(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	content := `fn helper() {}
fn main() {
	helper()
}`
	server.documents[uri] = content
	server.symbols[uri] = []symbolInfo{
		{Name: "helper", Kind: "fn", Line: 0, Col: 3},
		{Name: "main", Kind: "fn", Line: 1, Col: 3},
	}

	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "textDocument/definition",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.srv"},"position":{"line":2,"character":2}}`),
	}

	out := captureStdout(func() {
		server.handleMessage(msg)
	})

	resp := parseLSPResponse(t, out)
	var loc Location
	resBytes, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(resBytes, &loc); err != nil {
		t.Fatalf("Failed to parse definition location: %v", err)
	}

	if loc.URI != uri || loc.Range.Start.Line != 0 || loc.Range.Start.Character != 3 {
		t.Errorf("Expected definition to point to helper at 0:3, got URI=%q line=%d char=%d",
			loc.URI, loc.Range.Start.Line, loc.Range.Start.Character)
	}
}

func TestDiagnostics(t *testing.T) {
	server := NewServer()
	uri := "file:///test.srv"
	// Code has unused variable check warning
	content := `fn test() {
	let unused_var = 1
}`

	out := captureStdout(func() {
		server.analyzeAndPublishDiagnostics(uri, content)
	})

	// Format of response notification is a JSON-RPC notification
	// Content-Length: <n>\r\n\r\n{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":...}
	resp := parseLSPResponse(t, out)
	if resp.Method != "textDocument/publishDiagnostics" {
		t.Errorf("Expected method textDocument/publishDiagnostics, got %q", resp.Method)
	}

	var params struct {
		URI         string       `json:"uri"`
		Diagnostics []Diagnostic `json:"diagnostics"`
	}
	paramsBytes, _ := json.Marshal(resp.Params)
	if err := json.Unmarshal(paramsBytes, &params); err != nil {
		t.Fatalf("Failed to parse diagnostics params: %v", err)
	}

	if params.URI != uri {
		t.Errorf("Expected diagnostics URI to match, got %q", params.URI)
	}

	foundWarning := false
	for _, d := range params.Diagnostics {
		if strings.Contains(d.Message, "declared but never used") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("Expected unused variable diagnostic warning, got: %+v", params.Diagnostics)
	}
}

func TestDefinitionCrossFileImport(t *testing.T) {
	// Create two temporary .srv files representing imported module and importing module
	tmpDir := t.TempDir()
	importedFile := filepath.Join(tmpDir, "imported.srv")
	importingFile := filepath.Join(tmpDir, "main.srv")

	err := os.WriteFile(importedFile, []byte("fn externalHelper() {}"), 0644)
	if err != nil {
		t.Fatalf("Failed to write imported file: %v", err)
	}

	importingContent := "import \"imported.srv\"\nfn start() {\n\texternalHelper()\n}"
	err = os.WriteFile(importingFile, []byte(importingContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write importing file: %v", err)
	}

	server := NewServer()
	mainURI := "file://" + filepath.ToSlash(importingFile)
	server.documents[mainURI] = importingContent

	// Cursor positioned at the invocation of externalHelper
	msg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "textDocument/definition",
		Params:  json.RawMessage(fmt.Sprintf(`{"textDocument":{"uri":%q},"position":{"line":2,"character":4}}`, mainURI)),
	}

	out := captureStdout(func() {
		server.handleMessage(msg)
	})

	resp := parseLSPResponse(t, out)
	var loc Location
	resBytes, _ := json.Marshal(resp.Result)
	if err := json.Unmarshal(resBytes, &loc); err != nil {
		t.Fatalf("Failed to parse definition location: %v", err)
	}

	expectedURI := "file://" + filepath.ToSlash(importedFile)
	if !strings.EqualFold(loc.URI, expectedURI) {
		t.Errorf("Expected definition to point to imported file %q, got %q", expectedURI, loc.URI)
	}
}

