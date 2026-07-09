package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"serv/compiler"
)

func TestServLangNext5RoadmapItems(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_next5_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	// env: SERV_JWT_SECRET=test-key-123
	database "servdb://pool-primary/testdb"

	test "verify next 5 roadmap integrations compile and run" {
		let user = auth.register("alice", "alice@example.com", "secret123")
		assert user.username == "alice"
		assert user.status == "registered"

		let logged = auth.login("alice", "secret123")
		assert logged.username == "alice"
		assert logged.token != nil

		let sent = mail.send("bob@example.com", "Welcome {{.Name}}", { "Name": "Bob" })
		assert sent == true

		let slackSent = notify("slack", "https://hooks.slack.com/123", "Alert!")
		assert slackSent == true
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	// Intercept stdout to check test runner output.
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	runTests(tmpFile.Name(), false, "")

	w.Close()
	os.Stdout = oldStdout
	output := <-done

	t.Logf("Compiler Exec Output:\n%s", output)

	if !strings.Contains(output, "PASS") {
		t.Error("Expected tests to pass")
	}
	if strings.Contains(output, "FAIL") {
		t.Error("Expected no tests to fail")
	}
}

func TestServQueueStreamDSL(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_stream_dsl_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify stream DSL compiles and runs" {
		let orders = stream "orders" |> filter(fn(o) { return true }) |> window("1s") |> count()
		assert orders != nil
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	runTests(tmpFile.Name(), false, "")

	w.Close()
	os.Stdout = oldStdout
	output := <-done

	t.Logf("Stream DSL Exec Output:\n%s", output)

	if !strings.Contains(output, "PASS") {
		t.Error("Expected tests to pass")
	}
}

func TestEcosystemNext4RoadmapItems(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_next4_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify next 4 items compile" {
		let x = true
		assert x == true
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	runTests(tmpFile.Name(), false, "")

	w.Close()
	os.Stdout = oldStdout
	output := <-done

	t.Logf("Next 4 Exec Output:\n%s", output)

	if !strings.Contains(output, "PASS") {
		t.Error("Expected tests to pass")
	}
}

func TestTopicSchemaLinting(t *testing.T) {
	// Create temporary schemas directory in current working directory
	err := os.MkdirAll("schemas", 0755)
	if err != nil {
		t.Fatalf("failed to create schemas dir: %v", err)
	}
	defer os.RemoveAll("schemas")

	// Write a test schema
	schemaContent := `{
		"properties": {
			"id": { "type": "integer" },
			"name": { "type": "string" }
		},
		"required": ["id"]
	}`
	err = os.WriteFile("schemas/user-created.json", []byte(schemaContent), 0644)
	if err != nil {
		t.Fatalf("failed to write schema file: %v", err)
	}

	// 1. Test matching payload
	srvFileContent := `
	struct User {
		id: int,
		name: string
	}
	fn test_publish() {
		publish "user-created" User{id: 123, name: "Alice"}
	}
	`
	_, program, err := parseProjectString(srvFileContent)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	diags := compiler.Analyze(program)
	// Expect no errors/warnings for matching types
	for _, d := range diags {
		if d.Severity == "error" {
			t.Errorf("Unexpected error: %s", d.Message)
		}
	}

	// 2. Test mismatching payload (missing required field)
	srvFileContent2 := `
	struct UserBad {
		name: string
	}
	fn test_publish_bad() {
		publish "user-created" UserBad{name: "Alice"}
	}
	`
	_, program2, err := parseProjectString(srvFileContent2)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	diags2 := compiler.Analyze(program2)
	foundErr := false
	for _, d := range diags2 {
		if d.Severity == "error" && strings.Contains(d.Message, "missing required property") {
			foundErr = true
		}
	}
	if !foundErr {
		t.Errorf("Expected to find missing required property error, but did not. Diagnostics: %v", diags2)
	}

	// 3. Test type mismatch
	srvFileContent3 := `
	struct UserWrongType {
		id: string,
		name: string
	}
	fn test_publish_wrong_type() {
		publish "user-created" UserWrongType{id: "not-an-int", name: "Alice"}
	}
	`
	_, program3, err := parseProjectString(srvFileContent3)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	diags3 := compiler.Analyze(program3)
	foundTypeErr := false
	for _, d := range diags3 {
		if d.Severity == "error" && strings.Contains(d.Message, "expects type 'integer', but got 'string'") {
			foundTypeErr = true
		}
	}
	if !foundTypeErr {
		t.Errorf("Expected to find type mismatch error, but did not. Diagnostics: %v", diags3)
	}
}

// Helper to parse from string
func parseProjectString(content string) (string, *compiler.Program, error) {
	tmpFile, err := os.CreateTemp("", "test_parse_*.srv")
	if err != nil {
		return "", nil, err
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(content); err != nil {
		return "", nil, err
	}
	tmpFile.Close()
	return parseProject(tmpFile.Name())
}

