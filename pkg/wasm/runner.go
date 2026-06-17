package wasm

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

type MiddlewareManager struct {
	runtime wazero.Runtime
	mu      sync.Mutex
	cache   map[string]wazero.CompiledModule
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
		r := wazero.NewRuntimeWithConfig(ctx, rCfg)

		if _, instErr := wasi_snapshot_preview1.Instantiate(ctx, r); instErr != nil {
			err = fmt.Errorf("wasm: failed to instantiate WASI: %w", instErr)
			return
		}

		globalManager = &MiddlewareManager{
			runtime: r,
			cache:   make(map[string]wazero.CompiledModule),
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
		_ = old.Close(ctx)
	}

	m.cache[name] = compiled
	return nil
}

// Run executes the named middleware, passing headers or body via stdin and returning stdout.
func (m *MiddlewareManager) Run(ctx context.Context, name string, input []byte) ([]byte, error) {
	m.mu.Lock()
	compiled, exists := m.cache[name]
	m.mu.Unlock()

	if !exists {
		return nil, fmt.Errorf("wasm: middleware %s not registered", name)
	}

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeoutSec*time.Second)
	defer cancel()

	stdin := bytes.NewReader(input)
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
				return nil, fmt.Errorf("wasm: exited with code %d: %s", exitErr.ExitCode(), stderr.String())
			}
		} else {
			return nil, fmt.Errorf("wasm: execution failed: %w", err)
		}
	} else {
		_ = mod.Close(ctx)
	}

	return stdout.Bytes(), nil
}
