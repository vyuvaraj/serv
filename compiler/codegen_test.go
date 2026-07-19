package compiler

import (
	"strings"
	"testing"
)

func TestCodegenErrorPropagation(t *testing.T) {
	input := `
	fn loadProfile(id: int) -> error {
		let user = findUser(id)?
		return nil
	}
	`
	l := NewLexer(input)
	p := NewParser(l)
	program := p.ParseProgram()

	if len(p.Errors()) > 0 {
		t.Fatalf("Parser had errors: %v", p.Errors())
	}

	cg := NewCodegen(program)
	output, err := cg.Generate()
	if err != nil {
		t.Fatalf("Codegen failed: %v", err)
	}

	// Verify that error propagation generates direct error returns rather than interface slices
	if !strings.Contains(output, "return _prop_err_") {
		t.Errorf("Expected output to contain direct error return '_prop_err_', got:\n%s", output)
	}
	if strings.Contains(output, "return [2]interface{}{nil, _prop_err_") {
		t.Errorf("Expected output NOT to contain 2-element interface slice for error returns, got:\n%s", output)
	}
}

func TestCodegenSpawnWithSemaphore(t *testing.T) {
	input := `
	fn runTasks() {
		spawn "task1" limit 3 {
			log.info("task 1 execution")
		}
		spawn "task2" limit 5 {
			log.info("task 2 execution")
		}
	}
	`
	l := NewLexer(input)
	p := NewParser(l)
	program := p.ParseProgram()

	if len(p.Errors()) > 0 {
		t.Fatalf("Parser had errors: %v", p.Errors())
	}

	cg := NewCodegen(program)
	output, err := cg.Generate()
	if err != nil {
		t.Fatalf("Codegen failed: %v", err)
	}

	// Verify unique trace variable names to prevent redefinition errors
	if !strings.Contains(output, "_spawnTrace_3_3") {
		t.Errorf("Expected output to contain unique trace variable '_spawnTrace_3_3', got:\n%s", output)
	}
	if !strings.Contains(output, "_spawnTrace_6_3") {
		t.Errorf("Expected output to contain unique trace variable '_spawnTrace_6_3', got:\n%s", output)
	}
}
