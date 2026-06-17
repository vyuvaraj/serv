package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	DefaultMemLimitMB = 64
	DefaultTimeoutSec = 10
	wasmPageBytes     = 65536 // 64 KiB
)

type WasmManager struct {
	runtime wazero.Runtime
	mu      sync.Mutex
}

var (
	globalManager *WasmManager
	once          sync.Once
)

func GetWasmManager(ctx context.Context) (*WasmManager, error) {
	var err error
	once.Do(func() {
		memLimitPages := uint32((uint64(DefaultMemLimitMB) * 1024 * 1024) / wasmPageBytes)
		rCfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(memLimitPages)
		r := wazero.NewRuntimeWithConfig(ctx, rCfg)

		// Instantiate WASI snapshot
		if _, instErr := wasi_snapshot_preview1.Instantiate(ctx, r); instErr != nil {
			err = fmt.Errorf("wasm: failed to instantiate WASI: %w", instErr)
			return
		}

		globalManager = &WasmManager{
			runtime: r,
		}
	})
	return globalManager, err
}

func (m *WasmManager) Compile(ctx context.Context, wasmBytes []byte) (wazero.CompiledModule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime.CompileModule(ctx, wasmBytes)
}

func (m *WasmManager) RunTransform(ctx context.Context, compiled wazero.CompiledModule, message string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeoutSec*time.Second)
	defer cancel()

	stdin := bytes.NewReader([]byte(message))
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	modCfg := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithName("")

	m.mu.Lock()
	mod, err := m.runtime.InstantiateModule(ctx, compiled, modCfg)
	m.mu.Unlock()

	if err != nil {
		var exitErr *sys.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() != 0 {
				return "", fmt.Errorf("wasm: exited with code %d: %s", exitErr.ExitCode(), stderr.String())
			}
		} else {
			return "", fmt.Errorf("wasm: instantiation failed: %w", err)
		}
	} else {
		defer mod.Close(ctx)

		// Direct Memory Optimizations:
		// Check if module exports allocation and transformation entry points to bypass standard pipes.
		allocFn := mod.ExportedFunction("allocate")
		if allocFn == nil {
			allocFn = mod.ExportedFunction("malloc")
		}
		transformFn := mod.ExportedFunction("transform")

		if allocFn != nil && transformFn != nil {
			msgBytes := []byte(message)
			size := uint64(len(msgBytes))

			// Allocate space in guest memory
			results, allocErr := allocFn.Call(ctx, size)
			if allocErr == nil && len(results) > 0 {
				ptr := results[0]
				// Write directly to guest memory space
				if mod.Memory().Write(uint32(ptr), msgBytes) {
					// Invoke the transform function (transform(ptr, len))
					resResults, transErr := transformFn.Call(ctx, ptr, size)
					if transErr == nil && len(resResults) > 0 {
						resPtr := resResults[0]
						// The return value is a 64-bit integer packing: (ptr << 32) | len
						retPtr := uint32(resPtr >> 32)
						retLen := uint32(resPtr)
						
						if outBytes, readOk := mod.Memory().Read(retPtr, retLen); readOk {
							return string(outBytes), nil
						}
					}
				}
			}
		}
	}

	return stdout.String(), nil
}
