package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildReachabilityCheck(t *testing.T) {
	// 1. Create a temp directory
	tempDir, err := os.MkdirTemp("", "serv_build_reach_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 2. Write a .srv file with an unreachable postgres database connection
	srvPath := filepath.Join(tempDir, "service.srv")
	srvContent := `
	database "postgres://localhost:59999/testdb"
	server "8080"
	`
	if err := os.WriteFile(srvPath, []byte(srvContent), 0644); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}

	// 3. Compile without offline flag (should fail reachability check on port 59999).
	// Set BuildSkipCICheck so the GITHUB_ACTIONS guard doesn't bypass the check in CI.
	BuildSkipCICheck = true
	defer func() { BuildSkipCICheck = false }()
	BuildOffline = false
	_, err = buildServNoExit(srvPath, "test_service.exe", "", "", "", "")
	if err == nil {
		t.Error("expected build to fail due to unreachable database connection")
	} else if !strings.Contains(err.Error(), "infrastructure reachability check failed") {
		t.Errorf("expected reachability check failure message, got: %v", err)
	}

	// 4. Compile WITH offline flag (should succeed and skip reachability check)
	BuildOffline = true
	_, err = buildServNoExit(srvPath, "test_service.exe", "", "", "", "")
	if err != nil {
		t.Errorf("expected build to succeed when BuildOffline is true, got error: %v", err)
	}
}
