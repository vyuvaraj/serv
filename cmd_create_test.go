package main

import (
	"os"
	"testing"
)

func TestAIScaffoldWrite(t *testing.T) {
	// Clean up environment and file targets
	os.Remove("main.srv")

	// Set mock key so runtime does not log warn / fail on complete stub checks
	os.Setenv("OPENAI_API_KEY", "mock-key")
	os.Setenv("SERV_AI_CONNECTION", "openai://gpt-4o-mini")
	os.Setenv("SERV_TEST_AI_RESPONSE", "server \"8080\"\nroute \"GET\" \"/\" (req) { return \"ok\"; }")

	// Run scaffold command mock
	runAIScaffold("Create an API serving user details")

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
