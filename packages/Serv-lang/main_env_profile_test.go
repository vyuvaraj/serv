package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentProfileYAMLConfig(t *testing.T) {
	// 1. Create a temporary .srv test file
	tmpFile, err := os.CreateTemp("", "test_env_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp srv file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
test "environment profile config checks" {
	let mode = config("app.mode")
	let db = config("app.db_url")

	// Verify that the environment profile values were loaded instead of default
	assert mode == "staging-env"
	assert db == "postgres://staging_db"
}
`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv test file: %v", err)
	}
	tmpFile.Close()

	// 2. Resolve the build directory for the temp srv file
	absPath, err := filepath.Abs(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}
	buildDir := buildDirFor(absPath)
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		t.Fatalf("failed to create build directory: %v", err)
	}

	// 3. Create a profile specific config file inside the build directory
	stagingConfig := filepath.Join(buildDir, "config.staging.yml")
	stagingContent := `
app:
  mode: "staging-env"
  db_url: "postgres://staging_db"
`
	if err := os.WriteFile(stagingConfig, []byte(stagingContent), 0644); err != nil {
		t.Fatalf("failed to write staging config: %v", err)
	}
	defer os.Remove(stagingConfig)

	// 4. Create default config file inside the build directory
	defaultConfig := filepath.Join(buildDir, "config.yml")
	defaultContent := `
app:
  mode: "default-env"
  db_url: "sqlite://default_db"
`
	if err := os.WriteFile(defaultConfig, []byte(defaultContent), 0644); err != nil {
		t.Fatalf("failed to write default config: %v", err)
	}
	defer os.Remove(defaultConfig)

	// 5. Set SERV_ENV environment variable
	os.Setenv("SERV_ENV", "staging")
	defer os.Unsetenv("SERV_ENV")

	// 6. Run the compiler test runner
	// Capture stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r)
		done <- buf.String()
	}()

	runTests(tmpFile.Name(), false, "")

	w.Close()
	os.Stdout = oldStdout
	output := <-done

	t.Logf("Test output:\n%s", output)

	if !strings.Contains(output, "PASS") {
		t.Error("Expected tests to pass under 'staging' environment profile")
	}
	if strings.Contains(output, "FAIL") {
		t.Error("Expected no tests to fail")
	}
}
