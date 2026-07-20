package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35ErgonomicsAndGoInterop(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_ergonomics_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	@inline go fn reverseString(input string) string {
		import "strings"
		runes := []rune(input)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes)
	}

	test "verify phase 35 language ergonomics and go interop" {
		let reversed = reverseString("hello")
		assert reversed == "olleh"

		let res = exec.run("echo hello_exec")
		assert res.exitCode == 0
		assert res.stdout.trim() == "hello_exec"

		let tempPath = "./temp_test_phase35.txt"
		let writeOk = file.write(tempPath, "phase35_success")
		assert writeOk == true

		let exists = file.exists(tempPath)
		assert exists == true

		let content = file.read(tempPath)
		assert content == "phase35_success"

		let files = file.list(".")
		assert files.length() > 0
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	// Clean up any left over file
	defer os.Remove("./temp_test_phase35.txt")

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
		t.Error("Expected test execution to pass")
	}
}
