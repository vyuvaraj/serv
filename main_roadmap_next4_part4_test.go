package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespacesPart4(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_part4_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify phase 35 utility namespaces part 4" {
		// 1. Optional Chaining
		let user = nil
		let city = user?.address?.city
		assert city == nil

		// 2. Array Spread Operator
		let arr1 = [1, 2]
		let arr2 = [4, 5]
		let combined = [...arr1, 3, ...arr2]
		assert combined.length() == 5
		assert combined[0] == 1
		assert combined[2] == 3
		assert combined[4] == 5

		// 3. Time Namespace Overhaul
		let t1 = time.parse("2026-07-18T12:00:00Z", time.RFC3339)
		let dateStr = time.format(t1, time.DATE)
		assert dateStr == "2026-07-18"

		let t2 = time.add(t1, "2h")
		let timeStr = time.format(t2, time.RFC3339)
		assert timeStr == "2026-07-18T14:00:00Z"

		let diff = time.sub(t2, t1)
		assert diff == 7200.0 // 2 hours in seconds

		let isBefore = time.before(t1, t2)
		assert isBefore == true
		let isAfter = time.after(t2, t1)
		assert isAfter == true

		let comp = time.components(t1)
		assert comp.year == 2026.0
		assert comp.month == 7.0
		assert comp.day == 18.0
		assert comp.weekday == "Saturday"

		// 4. Multiline String Dedentation
		let str = ` + "`" + `
			SELECT *
			FROM users
			WHERE id = 42
		` + "`" + `
		// Check that leading whitespace is stripped and lines are correctly dedented
		let lines = str.split("\n")
		// Line 0 is empty, Line 1 is "SELECT *", Line 2 is "FROM users", Line 3 is "WHERE id = 42"
		assert lines[1].trim() == "SELECT *"
		assert lines[1].startsWith("SELECT") == true
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	// Compile and run the test using serv compiler (make sure TESTING=true is passed to localhost bind)
	cmd := exec.Command("go", "run", ".", "test", tmpFile.Name())
	cmd.Env = append(os.Environ(), "TESTING=true", "SERV_ENV=test")
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
		t.Error("Expected test execution to pass")
	}
}
