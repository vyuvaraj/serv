package broker

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWasmManagerInitialization(t *testing.T) {
	ctx := context.Background()
	mgr, err := GetWasmManager(ctx)
	if err != nil {
		t.Fatalf("failed to initialize WasmManager: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil WasmManager")
	}
}

func TestWasmTimeoutEnforcement(t *testing.T) {
	ctx := context.Background()
	mgr, err := GetWasmManager(ctx)
	if err != nil {
		t.Fatalf("failed to get WasmManager: %v", err)
	}

	compiled, err := mgr.Compile(ctx, noopWasm)
	if err != nil {
		t.Fatalf("failed to compile noopWasm: %v", err)
	}

	// Create a context that is already timed out
	canceledCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
	time.Sleep(2 * time.Millisecond)
	cancel()

	_, err = mgr.RunTransform(canceledCtx, compiled, "test message", "")
	if err == nil {
		t.Fatal("expected error on timed out context, got nil")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected context deadline error, got: %v", err)
	}
}

func TestWasmNoopTransformExecution(t *testing.T) {
	ctx := context.Background()
	mgr, err := GetWasmManager(ctx)
	if err != nil {
		t.Fatalf("failed to get WasmManager: %v", err)
	}

	compiled, err := mgr.Compile(ctx, noopWasm)
	if err != nil {
		t.Fatalf("failed to compile noopWasm: %v", err)
	}

	out, err := mgr.RunTransform(ctx, compiled, "hello payload", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if err != nil {
		t.Fatalf("RunTransform failed: %v", err)
	}
	_ = out // valid execution without panics
}
