// Package wasm provides a sandboxed WebAssembly execution engine built on wazero.
// It runs WASI command-module binaries with strict memory and time limits,
// piping caller-supplied bytes through stdin and returning the module's stdout.
//
// Design principles:
//   - Zero CGO — wazero is a pure-Go WASM runtime.
//   - Fresh isolation — a new Runtime instance is created for every invocation.
//   - Hard limits — memory pages and wall-clock timeout are enforced per call.
//   - No host-filesystem access — WASI is configured with an empty preopened
//     directory list, so modules cannot traverse the server's filesystem.
package wasm

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
	// DefaultMemLimitMB is the default WASM linear-memory ceiling in megabytes.
	DefaultMemLimitMB = 64
	// DefaultTimeoutSec is the default wall-clock execution timeout in seconds.
	DefaultTimeoutSec = 30

	wasmPageBytes = 65536 // 1 WASM page = 64 KiB
)

// Execute runs wasmBytes (a compiled WASI command module) in a fresh, isolated
// sandbox. input is piped to the module's stdin; the bytes written to stdout
// are returned. stderr is captured and included in any non-zero exit error.
//
// memLimitMB and timeoutSec are applied as hard ceilings; pass ≤0 for defaults.
func Execute(ctx context.Context, wasmBytes, input []byte, memLimitMB, timeoutSec int) ([]byte, error) {
	if memLimitMB <= 0 {
		memLimitMB = DefaultMemLimitMB
	}
	if timeoutSec <= 0 {
		timeoutSec = DefaultTimeoutSec
	}

	memLimitPages := uint32((uint64(memLimitMB) * 1024 * 1024) / wasmPageBytes)

	// Apply a hard wall-clock deadline to the entire invocation.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// Each call gets a brand-new Runtime — no shared mutable state between invocations.
	rCfg := wazero.NewRuntimeConfig().WithMemoryLimitPages(memLimitPages)
	r := wazero.NewRuntimeWithConfig(ctx, rCfg)
	defer r.Close(ctx) // nolint:errcheck

	// Instantiate the WASI host-module (provides fd_read, fd_write, proc_exit, etc.)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		return nil, fmt.Errorf("wasm: WASI instantiation failed: %w", err)
	}

	stdin := bytes.NewReader(input)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Disable all preopened directories — module has no host-filesystem access.
	modCfg := wazero.NewModuleConfig().
		WithStdin(stdin).
		WithStdout(stdout).
		WithStderr(stderr).
		WithName("transform")

	_, err := r.InstantiateWithConfig(ctx, wasmBytes, modCfg)
	if err != nil {
		// WASI command modules call proc_exit(0) on success, which wazero
		// surfaces as a *sys.ExitError with code 0. Treat that as success.
		var exitErr *sys.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() != 0 {
				return nil, fmt.Errorf("wasm: exited with code %d: %s", exitErr.ExitCode(), stderr.String())
			}
			// exit code 0 — fall through to return stdout
		} else {
			return nil, fmt.Errorf("wasm: execution failed: %w", err)
		}
	}

	return stdout.Bytes(), nil
}
