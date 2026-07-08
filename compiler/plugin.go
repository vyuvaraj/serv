package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"plugin"
)

// CompilerPlugin defines the interface that standard compiler plugins should satisfy.
// Plugins are compiled as Go plugins (.so/.dll) from .srv.plugin.go files.
type CompilerPlugin interface {
	Name() string
	Analyze(program *Program) []Diagnostic
}

// LoadAndRunPlugins searches the workspace directory for compiler plugins (.so/.dll),
// loads them dynamically, and runs their Analyze hooks on the AST.
func LoadAndRunPlugins(program *Program, workspaceDir string) []Diagnostic {
	var diags []Diagnostic

	// Find all .so files (on Linux/macOS) or .dll files (on Windows)
	pattern := filepath.Join(workspaceDir, "*.srv.plugin.so")
	if os.PathSeparator == '\\' {
		pattern = filepath.Join(workspaceDir, "*.srv.plugin.dll")
	}

	matches, err := filepath.Glob(pattern)
	if err != nil {
		return diags
	}

	for _, match := range matches {
		p, err := plugin.Open(match)
		if err != nil {
			// If dynamic loading is not supported in the running environment, log a warning diagnostic
			diags = append(diags, Diagnostic{
				Severity: "warning",
				Message:  fmt.Sprintf("Failed to open compiler plugin '%s': %v", filepath.Base(match), err),
			})
			continue
		}

		symPlugin, err := p.Lookup("Plugin")
		if err != nil {
			diags = append(diags, Diagnostic{
				Severity: "warning",
				Message:  fmt.Sprintf("Plugin symbol 'Plugin' not found in '%s'", filepath.Base(match)),
			})
			continue
		}

		if cp, ok := symPlugin.(CompilerPlugin); ok {
			pluginDiags := cp.Analyze(program)
			diags = append(diags, pluginDiags...)
		}
	}

	return diags
}
