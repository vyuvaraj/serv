package pipeline_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"servstore/pkg/pipeline"
	"servstore/pkg/storage"
)

// ── WASM helper ───────────────────────────────────────────────────────────────

// buildWASM compiles a Go source string to a WASI binary using the host toolchain.
// Skips the test when GOOS=wasip1 is unavailable (Go < 1.21).
func buildWASM(t *testing.T, src string) []byte {
	t.Helper()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "main.go")
	wasmPath := filepath.Join(tmpDir, "transform.wasm")

	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		t.Fatalf("buildWASM: write src: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", wasmPath, srcPath)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "unsupported") {
			t.Skipf("GOOS=wasip1 not supported: %s", out)
		}
		t.Fatalf("buildWASM: go build: %v\n%s", err, out)
	}
	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("buildWASM: read wasm: %v", err)
	}
	return data
}

// WASM source snippets used across tests.
const srcUppercase = `package main
import ("io"; "os"; "strings")
func main() { data, _ := io.ReadAll(os.Stdin); os.Stdout.WriteString(strings.ToUpper(string(data))) }
`

const srcPassthrough = `package main
import ("io"; "os")
func main() { io.Copy(os.Stdout, os.Stdin) }
`

const srcAppendTag = `package main
import ("io"; "os")
func main() { data, _ := io.ReadAll(os.Stdin); os.Stdout.Write(data); os.Stdout.WriteString("|tagged") }
`

const srcExitNonZero = `package main
import "os"
func main() { os.Stderr.WriteString("stage error"); os.Exit(1) }
`

// ── Store helper ──────────────────────────────────────────────────────────────

// newTestStore creates an isolated LocalStore in a temp directory and a bucket
// named "test". Returns the store and a cleanup function.
func newTestStore(t *testing.T) (storage.StorageEngine, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	bucket := "test"
	if err := store.CreateBucket(context.Background(), bucket); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	return store, bucket
}

// putObject stores raw bytes as an object in the bucket.
func putObject(t *testing.T, store storage.StorageEngine, bucket, key string, data []byte) {
	t.Helper()
	_, err := store.PutObject(context.Background(), bucket, key,
		bytes.NewReader(data), int64(len(data)), "application/octet-stream")
	if err != nil {
		t.Fatalf("PutObject %q: %v", key, err)
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestPipeline_SingleStage verifies a one-stage uppercase pipeline.
func TestPipeline_SingleStage(t *testing.T) {
	upperWasm := buildWASM(t, srcUppercase)
	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "upper.wasm", upperWasm)
	putObject(t, store, bucket, "input.txt", []byte("hello pipeline"))

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input:     "input.txt",
		Stages:    []pipeline.StageSpec{{Wasm: "upper.wasm"}},
		SaveTrace: true,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("expected status ok, got %q: %s", result.Status, result.Error)
	}
	if result.StagesRun != 1 {
		t.Errorf("expected StagesRun=1, got %d", result.StagesRun)
	}
	if len(result.Trace) != 1 {
		t.Fatalf("expected 1 trace entry, got %d", len(result.Trace))
	}
	if result.Trace[0].Status != "ok" {
		t.Errorf("trace[0] status = %q, want ok", result.Trace[0].Status)
	}
	if result.Trace[0].InputBytes != len("hello pipeline") {
		t.Errorf("trace[0].InputBytes = %d, want %d", result.Trace[0].InputBytes, len("hello pipeline"))
	}
}

// TestPipeline_MultiStage verifies a 3-stage pipeline: uppercase → passthrough → append tag.
func TestPipeline_MultiStage(t *testing.T) {
	upperWasm := buildWASM(t, srcUppercase)
	passWasm := buildWASM(t, srcPassthrough)
	tagWasm := buildWASM(t, srcAppendTag)

	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "upper.wasm", upperWasm)
	putObject(t, store, bucket, "pass.wasm", passWasm)
	putObject(t, store, bucket, "tag.wasm", tagWasm)
	putObject(t, store, bucket, "input.txt", []byte("hello"))

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input: "input.txt",
		Stages: []pipeline.StageSpec{
			{Wasm: "upper.wasm"},
			{Wasm: "pass.wasm"},
			{Wasm: "tag.wasm"},
		},
		OutputKey: "results/output.txt",
		SaveTrace: true,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if result.Status != "ok" {
		t.Fatalf("expected status ok, got %q: %s", result.Status, result.Error)
	}
	if result.StagesRun != 3 {
		t.Errorf("expected StagesRun=3, got %d", result.StagesRun)
	}
	if result.OutputKey != "results/output.txt" {
		t.Errorf("OutputKey = %q, want %q", result.OutputKey, "results/output.txt")
	}

	// Verify output was stored in bucket with correct content.
	out, err := store.GetObjectBytes(context.Background(), bucket, "results/output.txt", "")
	if err != nil {
		t.Fatalf("GetObjectBytes results/output.txt: %v", err)
	}
	expected := "HELLO|tagged"
	if string(out) != expected {
		t.Errorf("pipeline output = %q, want %q", string(out), expected)
	}
}

// TestPipeline_MissingInput verifies pre-flight failure when the input key does not exist.
func TestPipeline_MissingInput(t *testing.T) {
	upperWasm := buildWASM(t, srcUppercase)
	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "upper.wasm", upperWasm)
	// No input.txt uploaded.

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input:  "input.txt",
		Stages: []pipeline.StageSpec{{Wasm: "upper.wasm"}},
	})
	if err == nil {
		t.Fatal("expected error for missing input, got nil")
	}
	if result.Status != "error" {
		t.Errorf("expected status error, got %q", result.Status)
	}
}

// TestPipeline_MissingWASMKey verifies pre-flight failure when a WASM object key does not exist.
func TestPipeline_MissingWASMKey(t *testing.T) {
	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "input.txt", []byte("data"))
	// No WASM uploaded.

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input:  "input.txt",
		Stages: []pipeline.StageSpec{{Wasm: "nonexistent.wasm"}},
	})
	if err == nil {
		t.Fatal("expected error for missing WASM key, got nil")
	}
	if result.Status != "error" {
		t.Errorf("expected status error, got %q", result.Status)
	}
}

// TestPipeline_StageFailure verifies that a mid-pipeline stage failure returns a
// partial trace with the failing stage marked "error" and execution stopping.
func TestPipeline_StageFailure(t *testing.T) {
	passWasm := buildWASM(t, srcPassthrough)
	failWasm := buildWASM(t, srcExitNonZero)
	tagWasm := buildWASM(t, srcAppendTag)

	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "pass.wasm", passWasm)
	putObject(t, store, bucket, "fail.wasm", failWasm)
	putObject(t, store, bucket, "tag.wasm", tagWasm)
	putObject(t, store, bucket, "input.txt", []byte("data"))

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input: "input.txt",
		Stages: []pipeline.StageSpec{
			{Wasm: "pass.wasm"},
			{Wasm: "fail.wasm"}, // fails here
			{Wasm: "tag.wasm"},  // must NOT run
		},
		SaveTrace: true,
	})
	if err == nil {
		t.Fatal("expected error on failing stage, got nil")
	}
	if result.Status != "error" {
		t.Errorf("expected status error, got %q", result.Status)
	}
	if result.StagesRun != 2 {
		t.Errorf("expected StagesRun=2 (pass+fail), got %d", result.StagesRun)
	}
	if len(result.Trace) != 2 {
		t.Fatalf("expected 2 trace entries, got %d", len(result.Trace))
	}
	if result.Trace[0].Status != "ok" {
		t.Errorf("trace[0] (pass) should be ok, got %q", result.Trace[0].Status)
	}
	if result.Trace[1].Status != "error" {
		t.Errorf("trace[1] (fail) should be error, got %q", result.Trace[1].Status)
	}
}

// TestPipeline_MaxStagesExceeded verifies that requests with too many stages are rejected.
func TestPipeline_MaxStagesExceeded(t *testing.T) {
	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "input.txt", []byte("data"))

	stages := make([]pipeline.StageSpec, pipeline.MaxStages+1)
	for i := range stages {
		stages[i] = pipeline.StageSpec{Wasm: "any.wasm"}
	}

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input:  "input.txt",
		Stages: stages,
	})
	if err == nil {
		t.Fatal("expected error for too many stages, got nil")
	}
	if result.Status != "error" {
		t.Errorf("expected status error, got %q", result.Status)
	}
}

// TestPipeline_EmptyStages verifies that a request with no stages is rejected.
func TestPipeline_EmptyStages(t *testing.T) {
	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "input.txt", []byte("data"))

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input:  "input.txt",
		Stages: []pipeline.StageSpec{},
	})
	if err == nil {
		t.Fatal("expected error for empty stages, got nil")
	}
	if result.Status != "error" {
		t.Errorf("expected status error, got %q", result.Status)
	}
}

// TestPipeline_OutputKeyStoredTooBucket verifies that output_key persists the
// final result and can be retrieved via GetObjectBytes.
func TestPipeline_OutputKeyStoredToBucket(t *testing.T) {
	passWasm := buildWASM(t, srcPassthrough)
	store, bucket := newTestStore(t)
	putObject(t, store, bucket, "pass.wasm", passWasm)
	input := []byte("persist me")
	putObject(t, store, bucket, "input.txt", input)

	exec := pipeline.NewExecutor(store)
	result, err := exec.Run(context.Background(), bucket, pipeline.PipelineRequest{
		Input:     "input.txt",
		Stages:    []pipeline.StageSpec{{Wasm: "pass.wasm"}},
		OutputKey: "out/persisted.bin",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.OutputKey != "out/persisted.bin" {
		t.Errorf("OutputKey = %q, want %q", result.OutputKey, "out/persisted.bin")
	}
	got, err := store.GetObjectBytes(context.Background(), bucket, "out/persisted.bin", "")
	if err != nil {
		t.Fatalf("GetObjectBytes: %v", err)
	}
	if !bytes.Equal(got, input) {
		t.Errorf("stored output = %q, want %q", got, input)
	}
}
