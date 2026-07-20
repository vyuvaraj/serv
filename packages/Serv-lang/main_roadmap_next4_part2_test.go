package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespacesPart2(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_part2_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify phase 35 utility namespaces part 2" {
		// 1. Test Regex Namespace
		let matched = regex.match("^\\d+$", "12345")
		assert matched == true
		let notMatched = regex.match("^\\d+$", "abc")
		assert notMatched == false

		let found = regex.find("\\d+", "abc123xyz")
		assert found == "123"

		let replaced = regex.replace("apple|orange", "I love apple and orange", "fruit")
		assert replaced == "I love fruit and fruit"

		// 2. Test Math Namespace
		assert math.floor(3.7) == 3.0
		assert math.ceil(3.2) == 4.0
		assert math.round(3.5) == 4.0
		assert math.round(3.4) == 3.0
		assert math.abs(-5.5) == 5.5
		assert math.pow(2.0, 3.0) == 8.0
		assert math.sqrt(9.0) == 3.0
		assert math.min(10, 20) == 10.0
		assert math.max(10, 20) == 20.0

		// 3. Test Encoding Namespace
		let b64 = encoding.base64.encode("hello world")
		assert b64 == "aGVsbG8gd29ybGQ="
		let decodedB64 = encoding.base64.decode(b64)
		assert decodedB64 == "hello world"

		let h = encoding.hex.encode("hello")
		assert h == "68656c6c6f"
		let decodedHex = encoding.hex.decode(h)
		assert decodedHex == "hello"

		// 4. Test Hash Namespace
		let md5Val = hash.md5("hello")
		assert md5Val == "5d41402abc4b2a76b9719d911017c592"

		let sha256Val = hash.sha256("hello")
		assert sha256Val == "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

		let sha512Val = hash.sha512("hello")
		assert sha512Val.length() > 0

		let hmacVal = hash.hmac("secret", "hello", "sha256")
		assert hmacVal.length() > 0
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
		t.Error("Expected test execution to pass")
	}
}
