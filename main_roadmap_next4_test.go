package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespaces(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify phase 35 utility namespaces" {
		// 1. Test CSV Namespace
		let csvData = "name,role,level\nalice,admin,10\nbob,user,5"
		let parsedCSV = csv.parse(csvData)
		assert parsedCSV.length() == 3
		assert parsedCSV[1][0] == "alice"
		assert parsedCSV[2][2] == "5"

		let stringifiedCSV = csv.stringify(parsedCSV, nil)
		assert stringifiedCSV.includes("alice")

		// 2. Test XML Namespace
		let xmlData = "<user><name>Alice</name><role>Admin</role><groups><group>A</group><group>B</group></groups></user>"
		let parsedXML = xml.parse(xmlData)
		assert parsedXML.user.name == "Alice"
		assert parsedXML.user.role == "Admin"
		assert parsedXML.user.groups.group[0] == "A"
		assert parsedXML.user.groups.group[1] == "B"

		let stringifiedXML = xml.stringify(parsedXML)
		assert stringifiedXML.includes("Alice")

		// 3. Test YAML Namespace
		let yamlData = "user:\n  name: Alice\n  role: Admin\n  tags:\n    - dev\n    - sec"
		let parsedYAML = yaml.parse(yamlData)
		assert parsedYAML.user.name == "Alice"
		assert parsedYAML.user.tags[0] == "dev"

		let stringifiedYAML = yaml.stringify(parsedYAML)
		assert stringifiedYAML.includes("sec")

		// 4. Test Path Namespace
		let joined = path.join("dir", "subdir", "file.txt")
		assert joined.includes("file.txt")

		let dir = path.dirname("/a/b/c.txt")
		assert dir.replace("\\", "/") == "/a/b"

		let base = path.basename("/a/b/c.txt")
		assert base == "c.txt"

		let ext = path.ext("/a/b/c.txt")
		assert ext == ".txt"

		let abs = path.abs(".")
		assert abs.length() > 0
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
