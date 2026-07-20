package wasm_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/wasm"
)

// buildWASM compiles a Go source file to a WASI binary using the host Go toolchain.
// Skips the test if GOOS=wasip1 compilation is not available.
func buildWASM(t *testing.T, src string) []byte {
	t.Helper()

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "main.go")
	wasmPath := filepath.Join(tmpDir, "transform.wasm")

	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", wasmPath, srcPath)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		// wasip1 compilation requires Go 1.21+. Skip gracefully on older toolchains.
		if strings.Contains(string(out), "unsupported GOOS") || strings.Contains(string(out), "unsupported") {
			t.Skipf("GOOS=wasip1 not supported by this Go toolchain: %s", out)
		}
		t.Fatalf("go build wasip1 failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}
	return data
}

// TestExecute_Uppercase verifies that a WASM module that reads stdin and
// uppercases it produces the expected output.
func TestExecute_Uppercase(t *testing.T) {
	const src = `package main

import (
	"io"
	"os"
	"strings"
)

func main() {
	data, _ := io.ReadAll(os.Stdin)
	_, _ = os.Stdout.WriteString(strings.ToUpper(string(data)))
}
`
	wasmBytes := buildWASM(t, src)

	ctx := context.Background()
	input := []byte("hello servstore wasm")
	output, err := wasm.Execute(ctx, wasmBytes, input, 64, 30)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expected := "HELLO SERVSTORE WASM"
	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}

// TestExecute_PassThrough verifies identity transforms work correctly.
func TestExecute_PassThrough(t *testing.T) {
	const src = `package main

import (
	"io"
	"os"
)

func main() {
	io.Copy(os.Stdout, os.Stdin)
}
`
	wasmBytes := buildWASM(t, src)

	ctx := context.Background()
	input := []byte("servstore passthrough test 🚀")
	output, err := wasm.Execute(ctx, wasmBytes, input, 64, 30)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !bytes.Equal(output, input) {
		t.Errorf("expected passthrough output to equal input, got %q", output)
	}
}

// TestExecute_InvalidWASM verifies that malformed WASM bytes produce a clear error.
func TestExecute_InvalidWASM(t *testing.T) {
	ctx := context.Background()
	garbage := []byte("not-a-valid-wasm-binary")
	_, err := wasm.Execute(ctx, garbage, nil, 64, 30)
	if err == nil {
		t.Error("expected error for invalid WASM bytes, got nil")
	}
}

// TestExecute_MemoryLimit verifies that modules exceeding memory limits are rejected.
func TestExecute_MemoryLimit(t *testing.T) {
	// This module tries to allocate a 512 MB slice. With a 1 MB limit it should fail.
	const src = `package main

import "fmt"

func main() {
	buf := make([]byte, 512*1024*1024)
	fmt.Println(len(buf))
}
`
	wasmBytes := buildWASM(t, src)

	ctx := context.Background()
	_, err := wasm.Execute(ctx, wasmBytes, nil, 1 /* 1 MB limit */, 10)
	if err == nil {
		t.Error("expected memory-limit error, got nil")
	}
}

func TestExecute_ServLangWASM(t *testing.T) {
	wasmPath := "../../../Serv-lang/test_wasm.wasm"
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Skip("test_wasm.wasm not compiled, compile first using serv build")
	}

	ctx := context.Background()
	input := []byte("hello serv-lang wasm transform")
	output, err := wasm.Execute(ctx, wasmBytes, input, 64, 30)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expected := "WASM_TRANSFORMED: hello serv-lang wasm transform"
	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}

func TestExecute_ServLangComplexWASM(t *testing.T) {
	wasmPath := "../../../Serv-lang/test_wasm_complex.wasm"
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Skip("test_wasm_complex.wasm not compiled, compile first using serv build")
	}

	ctx := context.Background()
	input := []byte("complex hello")
	output, err := wasm.Execute(ctx, wasmBytes, input, 64, 30)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expected := "COMPLEX_RESULT: 24"
	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}
