package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespacesPart7(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_part7_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	extern fn new_mock(val) from "go:github.com/vyuvaraj/serv/packages/Serv-lang/runtime:NewMockReceiver"

	extern from "go:github.com/vyuvaraj/serv/packages/Serv-lang/runtime" {
		fn mock_func(str) from "MockFunc"
		fn mock_meth(obj, str) from "MockReceiver.MockMethod"
	}

	test "verify phase 35 utility namespaces part 7" {
		// 1. Diff Namespace
		let tDiff = diff.text("line 1\nline 2", "line 1\nline 3")
		assert tDiff == "  line 1\n- line 2\n+ line 3"

		let jDiff = diff.json({ "a": 1, "b": 2 }, { "a": 1, "b": 3, "c": 4 })
		assert jDiff.length() == 2.0

		// 2. Proto Namespace
		let schema = "syntax = \"proto3\"; message User { string name = 1; int32 age = 2; bool active = 3; }"
		let encoded = proto.encode({ "name": "bob", "age": 42, "active": true }, schema)
		let decoded = proto.decode(encoded, schema)
		assert decoded.name == "bob"
		assert decoded.age == 42.0
		assert decoded.active == true

		// 3. Extern Improvements (Block + Method receivers)
		let val = mock_func("hello")
		assert val == "mock:hello"

		let obj = new_mock("base_")
		let methVal = mock_meth(obj, "suffix")
		assert methVal == "base_suffix"
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

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
