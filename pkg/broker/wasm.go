package broker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// RunTransform executes a compiled WASI WebAssembly module on a message string.
// It pipes the message to stdin and returns the transformed stdout string.
func RunTransform(ctx context.Context, wasmBytes []byte, message string) (string, error) {
	memLimitPages := uint32((uint64(DefaultMemLimitMB) * 1024 * 1024) / wasmPageBytes)

	ctx, cancel := context.WithTimeout(ctx, DefaultTimeoutSec*time.Second)
	defer cancel()

	rCfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(memLimitPages)
	r := wazero.NewRuntimeWithConfig(ctx, rCfg)
	defer r.Close(ctx)

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return "", fmt.Errorf("wasm: WASI instantiation failed: %w", err)
	}

	stdin := bytes.NewReader([]byte(message))
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	modCfg := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithName("servqueue-transform")

	_, err := r.InstantiateWithConfig(ctx, wasmBytes, modCfg)
	if err != nil {
		var exitErr *sys.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() != 0 {
				return "", fmt.Errorf("wasm: exited with code %d: %s", exitErr.ExitCode(), stderr.String())
			}
		} else {
			return "", fmt.Errorf("wasm: execution failed: %w", err)
		}
	}

	return stdout.String(), nil
}
