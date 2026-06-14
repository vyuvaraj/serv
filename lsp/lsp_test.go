package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
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
