package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWASMEdgeTargetCompilation(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_wasm_edge_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
let input = wasm.readInput()
let output = f"WASM_TRANSFORMED: {input}"
wasm.writeOutput(output)
`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	outputWasm := filepath.Join(filepath.Dir(tmpFile.Name()), "test_wasm_edge.wasm")
	defer os.Remove(outputWasm)

	// Compile to target wasm-edge
	_, err = buildServNoExit(tmpFile.Name(), outputWasm, "wasm-edge", "", "", "")
	if err != nil {
		t.Fatalf("Build failed for wasm-edge target: %v", err)
	}

	// Verify target wasm-edge compiled file exists
	if _, err := os.Stat(outputWasm); err != nil {
		t.Errorf("Expected compiled WASM file to exist at %s: %v", outputWasm, err)
	}
}
