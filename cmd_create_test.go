package main

import (
	"os"
	"strings"
	"testing"
)

func TestAIScaffoldWrite(t *testing.T) {
	// Clean up environment and file targets
	os.Remove("main.srv")

	// Set mock key so runtime does not log warn / fail on complete stub checks
	os.Setenv("OPENAI_API_KEY", "mock-key")
	os.Setenv("SERV_AI_CONNECTION", "openai://gpt-4o-mini")
	os.Setenv("SERV_TEST_AI_RESPONSE", "server \"8080\"\nroute \"GET\" \"/\" (req) { return \"ok\" }")

	// Run scaffold command mock
	runAIScaffold("Create an API serving user details", false)

	// Verify main.srv was written
	if _, err := os.Stat("main.srv"); os.IsNotExist(err) {
		t.Fatalf("expected main.srv to be written by AI scaffolder")
	}

	content, err := os.ReadFile("main.srv")
	if err != nil {
		t.Fatalf("failed to read generated main.srv: %v", err)
	}

	if len(content) == 0 {
		t.Errorf("scaffolded file should not be empty")
	}

	// Clean up
	os.Remove("main.srv")
}

func TestAIScaffoldAutoFix(t *testing.T) {
	// Clean up target
	os.Remove("main.srv")

	// Set mock environment
	os.Setenv("OPENAI_API_KEY", "mock-key")
	os.Setenv("SERV_AI_CONNECTION", "openai://gpt-4o-mini")

	// Response 1: Valid Serv syntax but fails unit tests (assert x == 2)
	// Response 2: Corrected code passing the tests
	resp1 := `server "8080"
test "failing test" {
	let x = 1
	assert x == 2
}`
	resp2 := `server "8080"
test "passing test" {
	let x = 1
	assert x == 1
}`

	os.Setenv("SERV_TEST_AI_RESPONSE", resp1+"|||"+resp2)

	// Run with autoFix = true
	runAIScaffold("create API and write tests", true)

	// Verify main.srv was written and contains correct content from response 2
	if _, err := os.Stat("main.srv"); os.IsNotExist(err) {
		t.Fatalf("expected main.srv to be written by AI scaffolder auto-fix")
	}

	content, err := os.ReadFile("main.srv")
	if err != nil {
		t.Fatalf("failed to read main.srv: %v", err)
	}

	if !strings.Contains(string(content), "assert x == 1") {
		t.Errorf("expected generated file to contain the fixed assertion, got:\n%s", string(content))
	}

	// Clean up
	os.Remove("main.srv")
	os.Unsetenv("SERV_TEST_AI_RESPONSE")
}
