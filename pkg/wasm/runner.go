package wasm

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
	_ "github.com/tetratelabs/wazero/internal/fsapi"
	"github.com/tetratelabs/wazero/sys"
)

const (
	DefaultMemLimitMB = 64
	DefaultTimeoutSec = 10
	wasmPageBytes     = 65536 // 64 KiB
)

type MiddlewareManager struct {
	runtime wazero.Runtime
	mu      sync.Mutex
	cache   map[string]wazero.CompiledModule
	pools   map[string]chan api.Module
}

var (
	globalManager *MiddlewareManager
	once          sync.Once
)

func GetMiddlewareManager(ctx context.Context) (*MiddlewareManager, error) {
	var err error
	once.Do(func() {
		memLimitPages := uint32((uint64(DefaultMemLimitMB) * 1024 * 1024) / wasmPageBytes)
		rCfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(memLimitPages)

		// PS.2: Configure directory-backed compilation cache
		cacheDir := os.Getenv("SERV_WASM_CACHE_DIR")
		if cacheDir == "" {
			cacheDir = ".wazero-cache-servgate"
		}
		_ = os.MkdirAll(cacheDir, 0755)
		compCache, cacheErr := wazero.NewCompilationCacheWithDir(cacheDir)
		if cacheErr == nil {
			rCfg = rCfg.WithCompilationCache(compCache)
		}

		r := wazero.NewRuntimeWithConfig(ctx, rCfg)

		if _, instErr := wasi_snapshot_preview1.Instantiate(ctx, r); instErr != nil {
			err = fmt.Errorf("wasm: failed to instantiate WASI: %w", instErr)
			return
		}

		globalManager = &MiddlewareManager{
			runtime: r,
			cache:   make(map[string]wazero.CompiledModule),
			pools:   make(map[string]chan api.Module),
		}
	})
	return globalManager, err
}

func (m *MiddlewareManager) Register(ctx context.Context, name string, wasmBytes []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	compiled, err := m.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("wasm: compilation failed: %w", err)
	}

	if old, exists := m.cache[name]; exists {
		go func(c wazero.CompiledModule) {
			time.Sleep(5 * time.Second)
			_ = c.Close(context.Background())
		}(old)
	}

	m.cache[name] = compiled

	// Clean up old pool if it exists
	if oldPool, exists := m.pools[name]; exists {
		close(oldPool)
		for mod := range oldPool {
			_ = mod.Close(ctx)
		}
	}
	m.pools[name] = make(chan api.Module, 10)

	return nil
}

// Run executes the named middleware, passing headers or body via stdin and returning stdout.
func (m *MiddlewareManager) Run(ctx context.Context, name string, input []byte) ([]byte, error) {
	m.mu.Lock()
	compiled, exists := m.cache[name]
	pool, poolExists := m.pools[name]
	m.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("wasm: middleware %s not registered", name)
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeoutSec*time.Second)
	defer cancel()

	var mod api.Module
	reused := false

	// Try to get a pooled instance if it exists
	if poolExists {
		select {
		case inst, ok := <-pool:
			if ok {
				mod = inst
				reused = true
			}
		default:
		}
	}

	stdin := bytes.NewReader(input)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	modCfg := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithName("")

	if mod == nil {
		m.mu.Lock()
		var err error
		mod, err = m.runtime.InstantiateModule(ctx, compiled, modCfg)
		m.mu.Unlock()
		if err != nil {
			var exitErr *sys.ExitError
			if errors.As(err, &exitErr) {
				if exitErr.ExitCode() != 0 {
					return nil, fmt.Errorf("wasm: exited with code %d: %s", exitErr.ExitCode(), stderr.String())
				}
			} else {
				return nil, fmt.Errorf("wasm: execution failed: %w", err)
			}
		}
	}

	// Run logic
	if mod != nil {
		// Check for direct memory endpoints
		allocFn := mod.ExportedFunction("allocate")
		if allocFn == nil {
			allocFn = mod.ExportedFunction("malloc")
		}
		transformFn := mod.ExportedFunction("transform")
		if transformFn == nil {
			transformFn = mod.ExportedFunction("process")
		}

		if allocFn != nil && transformFn != nil {
			size := uint64(len(input))
			results, allocErr := allocFn.Call(ctx, size)
			if allocErr == nil && len(results) > 0 {
				ptr := results[0]
				if mod.Memory().Write(uint32(ptr), input) {
					resResults, transErr := transformFn.Call(ctx, ptr, size)
					if transErr == nil && len(resResults) > 0 {
						resPtr := resResults[0]
						retPtr := uint32(resPtr >> 32)
						retLen := uint32(resPtr)
						
						if outBytes, readOk := mod.Memory().Read(retPtr, retLen); readOk {
							outCopy := make([]byte, len(outBytes))
							copy(outCopy, outBytes)

							// Try to return to the pool
							if poolExists {
								select {
								case pool <- mod:
									return outCopy, nil
								default:
								}
							}
							_ = mod.Close(ctx)
							return outCopy, nil
						}
					}
				}
			}
		}

		// Fallback for standard I/O (cannot be pooled reliably because I/O is bound at init)
		if !reused {
			defer mod.Close(ctx)
		} else {
			// If we reused a standard I/O module and didn't hit direct memory, close it to be safe
			_ = mod.Close(ctx)
		}
	}

	return stdout.Bytes(), nil
}
