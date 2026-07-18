package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPhase35UtilityNamespacesPart5(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_utility_namespaces_part5_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
	test "verify phase 35 utility namespaces part 5" {
		// 1. JWT Namespace
		let secret = "super-secret-key-12345"
		let payload = { "sub": "alice", "admin": true }
		let token = jwt.sign(payload, secret)
		
		let claims = jwt.verify(token, secret)
		assert claims.sub == "alice"
		assert claims.admin == true

		let decoded = jwt.decode(token)
		assert decoded.sub == "alice"

		// 2. Compress Namespace
		let orig = "compression test message 123!!"
		let gz = compress.gzip(orig)
		let ungz = compress.ungzip(gz)
		assert ungz == orig

		let def = compress.deflate(orig)
		let inf = compress.inflate(def)
		assert inf == orig

		// 3. Semver Namespace
		let parsed = semver.parse("1.5.8-beta.2")
		assert parsed.major == 1.0
		assert parsed.minor == 5.0
		assert parsed.patch == 8.0

		assert semver.compare("1.2.3", "1.2.4") == -1.0
		assert semver.compare("2.0.0", "1.9.9") == 1.0
		assert semver.compare("3.1.2", "3.1.2") == 0.0

		assert semver.satisfies("^1.2.3", "1.8.9") == true
		assert semver.satisfies("^1.2.3", "2.0.0") == false
		assert semver.satisfies("~1.2.3", "1.2.4") == true
		assert semver.satisfies("~1.2.3", "1.3.0") == false
		assert semver.satisfies(">=2.0.0", "2.0.1") == true

		// 4. Duration Namespace
		let secs = duration.parse("2h30m")
		assert secs == 9000.0

		let fmtStr = duration.format(9000.0)
		assert fmtStr == "2h30m0s"

		let past = time.parse("2026-07-18T10:00:00Z", time.RFC3339)
		let elapsed = duration.since(past)
		assert elapsed >= 0.0
	}
	`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	cmd := exec.Command("go", "run", ".", "test", tmpFile.Name())
	cmd.Env = append(os.Environ(), "TESTING=true", "SERV_ENV=test")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String() + "\n" + stderr.String()
	t.Logf("Compiler Exec Output:\n%s", output)

	if err != nil {
		t.Fatalf("Compiler test execution failed: %v\nError output:\n%s", err, output)
	}

	if !strings.Contains(output, "PASS") {
		t.Error("Expected test execution to pass")
	}
}
