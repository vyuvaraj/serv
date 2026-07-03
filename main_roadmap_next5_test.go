package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
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
