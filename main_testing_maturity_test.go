package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"serv/runtime"
)

func TestMockingHTTPGet(t *testing.T) {
	runtime.ResetTestState()
	defer runtime.ResetTestState()

	// Register mock
	runtime.RegisterMock("runtime.HTTPGet:http://my-mocked-api.com/users", func(args ...interface{}) interface{} {
		return runtime.HTTPResponse{
			Status: 200,
			Body:   `{"status": "mocked"}`,
		}
	})

	// Invoke HTTPGet
	res := runtime.HTTPGet("http://my-mocked-api.com/users")
	httpResp, ok := res.(runtime.HTTPResponse)
	if !ok {
		t.Fatalf("Expected runtime.HTTPResponse, got %T", res)
	}

	if httpResp.Status != 200 {
		t.Errorf("Expected status 200, got %d", httpResp.Status)
	}
	if httpResp.Body != `{"status": "mocked"}` {
		t.Errorf("Expected mocked body, got %s", httpResp.Body)
	}
}

func TestMockingDBQuery(t *testing.T) {
	runtime.ResetTestState()
	defer runtime.ResetTestState()

	// Register mock
	runtime.RegisterMock("runtime.DBQuery:SELECT name FROM profiles", func(args ...interface{}) interface{} {
		return []interface{}{
			map[string]interface{}{"name": "Mocked User"},
		}
	})

	// Invoke DBQuery
	res := runtime.DBQuery("SELECT name FROM profiles")
	rows, ok := res.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{}, got %T", res)
	}

	if len(rows) != 1 {
		t.Fatalf("Expected 1 row, got %d", len(rows))
	}
	user := rows[0].(map[string]interface{})
	if user["name"] != "Mocked User" {
		t.Errorf("Expected 'Mocked User', got %s", user["name"])
	}
}

func TestTestIsolationReset(t *testing.T) {
	runtime.ResetTestState()

	// Set some mock and cache state
	runtime.RegisterMock("runtime.HTTPGet:http://test.com", func(args ...interface{}) interface{} {
		return "ok"
	})
	runtime.CacheSet("my_key", "my_val", "1m")

	// Verify they are set
	if _, ok := runtime.GetMock("runtime.HTTPGet:http://test.com"); !ok {
		t.Fatal("Expected mock to be set")
	}
	if runtime.CacheGet("my_key") != "my_val" {
		t.Fatal("Expected cache to contain my_val")
	}

	// Trigger Reset
	runtime.ResetTestState()

	// Verify they are cleared
	if _, ok := runtime.GetMock("runtime.HTTPGet:http://test.com"); ok {
		t.Error("Expected mock to be cleared by ResetTestState")
	}
	if runtime.CacheGet("my_key") != nil {
		t.Error("Expected cache to be cleared by ResetTestState")
	}
}

func TestMockIntegration(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_mock_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
test "my integration test" {
	mock http.get("http://api.local/data") {
		return "mocked value"
	}
	let val = http.get("http://api.local/data")
	assert val == "mocked value"
}
`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	// Run compiler tests on this file
	runTests(tmpFile.Name(), false, "")
}

func TestTestFiltering(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_filter_*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	srvContent := `
test "match test" {
	assert true
}
test "skip test" {
	assert false
}
`
	if _, err := tmpFile.WriteString(srvContent); err != nil {
		t.Fatalf("failed to write srv file: %v", err)
	}
	tmpFile.Close()

	// Capture stdout
	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w

	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	// We filter for "Match", so it should run "match test" and skip "skip test".
	// Since "skip test" is skipped, the runTests command should succeed and not crash/exit.
	runTests(tmpFile.Name(), false, "Match")

	w.Close()
	os.Stdout = oldStdout
	output := <-done

	t.Logf("Filtered Tests Output:\n%s", output)

	if !strings.Contains(output, "Test_MatchTest") {
		t.Error("Expected test output to contain Test_MatchTest")
	}
	if strings.Contains(output, "Test_SkipTest") {
		t.Error("Expected test output to NOT contain Test_SkipTest")
	}
}
