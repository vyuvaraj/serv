package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceDependenciesAndModules(t *testing.T) {
	// Root directory is 2 levels up from packages/ServShared/pkg
	rootDir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("failed to locate root dir: %v", err)
	}

	goWorkPath := filepath.Join(rootDir, "go.work")
	data, err := os.ReadFile(goWorkPath)
	if err != nil {
		t.Fatalf("failed to read go.work at %s: %v", goWorkPath, err)
	}

	content := string(data)
	expectedPackages := []string{
		"packages/Serv-lang",
		"packages/ServGate",
		"packages/ServQueue",
		"packages/ServStore",
		"packages/ServCache",
		"packages/ServAuth",
		"packages/ServConsole",
		"packages/ServMesh",
		"packages/ServCron",
		"packages/ServCloud",
		"packages/ServTrace",
		"packages/ServTunnel",
		"packages/ServPool",
		"packages/ServMail",
		"packages/ServFlow",
		"packages/ServRegistry",
		"packages/ServShared",
		"packages/servlockctl",
		"packages/servsecretctl",
	}

	for _, pkg := range expectedPackages {
		if !strings.Contains(content, pkg) {
			t.Errorf("expected go.work to register package %q", pkg)
		}

		goModFile := filepath.Join(rootDir, pkg, "go.mod")
		if _, err := os.Stat(goModFile); os.IsNotExist(err) {
			t.Errorf("expected go.mod to exist at %s", goModFile)
		}
	}
}
