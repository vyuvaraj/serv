package broker

import (
	"context"
	"testing"
)

// A minimal valid WASM binary representing a no-op module
var noopWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // Magic & Version
}

func BenchmarkWasmCompilationAndExecution(b *testing.B) {
	ctx := context.Background()
	mgr, err := GetWasmManager(ctx)
	if err != nil {
		b.Fatalf("Failed to initialize manager: %v", err)
	}

	compiled, err := mgr.Compile(ctx, noopWasm)
	if err != nil {
		b.Fatalf("Failed to compile WASM: %v", err)
	}

	b.ResetTimer()

	b.Run("WithCaching", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, err := mgr.RunTransform(ctx, compiled, "hello")
			if err != nil {
				b.Fatalf("Run failed: %v", err)
			}
		}
	})
}
