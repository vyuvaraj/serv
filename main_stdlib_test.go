package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestServLangStdlibBindings(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_stdlib_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	// env: SERV_JWT_SECRET=test-key-123
	database "servdb://pool-primary/testdb"
	cache "servcache://localhost:6379"
	broker "servqueue://localhost:8082"

	import { register, login } from "stdlib/auth.srv"
	import { query } from "stdlib/db.srv"
	import { set, get } from "stdlib/cache.srv"
	import { publishMessage } from "stdlib/queue.srv"

	test "verify first-class stdlib bindings compile and run" {
		let user = register("stdlib_user", "stdlib@example.com", "secret123")
		assert user.username == "stdlib_user"

		let logged = login("stdlib_user", "secret123")
		assert logged.token != nil

		set("test_key", "test_val", "10s")
		let cachedVal = get("test_key")
		assert cachedVal == "test_val"

		publishMessage("test_topic", "hello stomp")
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	cmd := exec.Command("go", "run", ".", "test", tmpFile.Name())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String() + "\n" + stderr.String()
	t.Logf("Compiler Exec Output:\n%s", output)

	if err != nil {
		t.Fatalf("Compiler test execution failed: %v\nError output:\n%s", err, output)
	}

	if !strings.Contains(output, "PASS") {
		t.Error("Expected standard library tests to pass")
	}
}
