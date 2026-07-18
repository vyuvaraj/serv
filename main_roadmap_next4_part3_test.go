package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespacesPart3(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_part3_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify phase 35 utility namespaces part 3" {
		// 1. Test UUID Namespace
		let u4 = uuid.v4()
		assert u4.length() == 36
		let u7 = uuid.v7()
		assert u7.length() == 36

		// 2. Test Rand Namespace
		let ri = rand.int(5, 10)
		assert ri >= 5.0
		assert ri <= 10.0

		let rf = rand.float()
		assert rf >= 0.0
		assert rf < 1.0

		let rs = rand.string(8)
		assert rs.length() == 8

		let rb = rand.bool()
		assert rb == true || rb == false

		// 3. Test URL Namespace
		let parsedURL = url.parse("https://user:pass@example.com:8080/path/to/resource?q=hello&a=1")
		assert parsedURL.scheme == "https"
		assert parsedURL.host == "example.com:8080"
		assert parsedURL.path == "/path/to/resource"
		assert parsedURL.query.q == "hello"
		assert parsedURL.query.a == "1"

		let encoded = url.encode("hello world/test?")
		assert encoded == "hello+world%2Ftest%3F"
		let decoded = url.decode(encoded)
		assert decoded == "hello world/test?"

		// 4. Test Env Namespace
		let got = env.get("PATH")
		assert got.length() > 0

		let gotDefaultInt = env.int("NON_EXISTING_INT_VAL_123", 42)
		assert gotDefaultInt == 42.0

		let gotDefaultBool = env.bool("NON_EXISTING_BOOL_VAL_123", true)
		assert gotDefaultBool == true
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
