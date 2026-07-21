package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	DefaultMemLimitMB = 64
	DefaultTimeoutMs  = 50
	wasmPageBytes     = 65536 // 64 KiB
)

type WasmManager struct {
	runtime wazero.Runtime
	mu      sync.Mutex
	pools   map[wazero.CompiledModule]chan api.Module
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

		// PS.2: Configure directory-backed compilation cache
		cacheDir := os.Getenv("SERV_WASM_CACHE_DIR")
		if cacheDir == "" {
			cacheDir = ".wazero-cache-servqueue"
		}
		_ = os.MkdirAll(cacheDir, 0755)
		compCache, cacheErr := wazero.NewCompilationCacheWithDir(cacheDir)
		if cacheErr == nil {
			rCfg = rCfg.WithCompilationCache(compCache)
		}

		r := wazero.NewRuntimeWithConfig(ctx, rCfg)

		// Instantiate WASI snapshot
		if _, instErr := wasi_snapshot_preview1.Instantiate(ctx, r); instErr != nil {
			err = fmt.Errorf("wasm: failed to instantiate WASI: %w", instErr)
			return
		}

		globalManager = &WasmManager{
			runtime: r,
			pools:   make(map[wazero.CompiledModule]chan api.Module),
		}
	})
	return globalManager, err
}

func (m *WasmManager) Compile(ctx context.Context, wasmBytes []byte) (wazero.CompiledModule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime.CompileModule(ctx, wasmBytes)
}

func (m *WasmManager) RunTransform(ctx context.Context, compiled wazero.CompiledModule, message string, traceparent string) (string, error) {
	timeout := 50 * time.Millisecond
	if customTimeout := os.Getenv("SERV_WASM_TIMEOUT_MS"); customTimeout != "" {
		if d, err := time.ParseDuration(customTimeout + "ms"); err == nil && d > 0 {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("wasm: execution context error: %w", err)
	}

	m.mu.Lock()
	pool, exists := m.pools[compiled]
	if !exists {
		pool = make(chan api.Module, 10)
		m.pools[compiled] = pool
	}
	m.mu.Unlock()

	var mod api.Module
	reused := false

	select {
	case inst := <-pool:
		mod = inst
		reused = true
	default:
	}

	stdin := bytes.NewReader([]byte(message))
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	modCfg := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithName("").
		WithEnv("TRACEPARENT", traceparent)

	if mod == nil {
		m.mu.Lock()
		var err error
		mod, err = m.runtime.InstantiateModule(ctx, compiled, modCfg)
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
		}
	}

	if err := ctx.Err(); err != nil {
		if mod != nil {
			_ = mod.Close(ctx)
		}
		return "", fmt.Errorf("wasm: execution context error: %w", err)
	}

	if mod != nil {
		allocFn := mod.ExportedFunction("allocate")
		if allocFn == nil {
			allocFn = mod.ExportedFunction("malloc")
		}
		transformFn := mod.ExportedFunction("transform")

		if allocFn != nil && transformFn != nil {
			msgBytes := []byte(message)
			size := uint64(len(msgBytes))

			results, allocErr := allocFn.Call(ctx, size)
			if allocErr == nil && len(results) > 0 {
				ptr := results[0]
				if mod.Memory().Write(uint32(ptr), msgBytes) {
					resResults, transErr := transformFn.Call(ctx, ptr, size)
					if transErr == nil && len(resResults) > 0 {
						resPtr := resResults[0]
						retPtr := uint32(resPtr >> 32)
						retLen := uint32(resPtr)
						
						if outBytes, readOk := mod.Memory().Read(retPtr, retLen); readOk {
							outStr := string(outBytes)
							
							// Try to return to the pool
							select {
							case pool <- mod:
								return outStr, nil
							default:
							}
							_ = mod.Close(ctx)
							return outStr, nil
						}
					}
				}
			}
		}

		if !reused {
			defer mod.Close(ctx)
		} else {
			_ = mod.Close(ctx)
		}
	}

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("wasm: execution context error: %w", err)
	}

	return stdout.String(), nil
}
