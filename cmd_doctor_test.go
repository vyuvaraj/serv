package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDoctorCommandOffline(t *testing.T) {
	// Set discovery pointing to non-existent ports (down services)
	discoveryMap := map[string]string{
		"gate": "http://127.0.0.1:59991",
		"store": "http://127.0.0.1:59992",
	}
	discBytes, _ := json.Marshal(discoveryMap)
	os.Setenv("SERVVERSE_DISCOVERY", string(discBytes))
	defer os.Unsetenv("SERVVERSE_DISCOVERY")

	// Set invalid trace endpoint to trigger a warning/error in telemetry pipeline
	os.Setenv("SERV_OTLP_ENDPOINT", "http://127.0.0.1:59993")
	defer os.Unsetenv("SERV_OTLP_ENDPOINT")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Mock osExit
	oldExit := osExit
	defer func() { osExit = oldExit }()
	exitCalled := false
	osExit = func(code int) {
		exitCalled = true
		panic("exit")
	}

	// Run doctor command in a separate function
	func() {
		defer func() {
			recover()
		}()
		runDoctor(false)
	}()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !exitCalled {
		t.Errorf("Expected osExit to be called due to offline services")
	}
	if !strings.Contains(output, "DOWN") {
		t.Errorf("Expected output to report DOWN status, got:\n%s", output)
	}
	if !strings.Contains(output, "Telemetry Pipeline Diagnostics") {
		t.Errorf("Expected output to run Telemetry check, got:\n%s", output)
	}
}

func TestDoctorCommandOnline(t *testing.T) {
	// Create mock services that return version/edition JSON
	mockGate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"version":"v0.1.0","edition":"enterprise"}`))
	}))
	defer mockGate.Close()

	mockStore := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"version":"v0.1.5","edition":"oss"}`))
	}))
	defer mockStore.Close()

	mockCollector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockCollector.Close()

	discoveryMap := map[string]string{
		"gate":  mockGate.URL,
		"store": mockStore.URL,
	}
	discBytes, _ := json.Marshal(discoveryMap)
	os.Setenv("SERVVERSE_DISCOVERY", string(discBytes))
	defer os.Unsetenv("SERVVERSE_DISCOVERY")

	os.Setenv("SERV_OTLP_ENDPOINT", mockCollector.URL)
	defer os.Unsetenv("SERV_OTLP_ENDPOINT")

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runDoctor(false)

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	fmt.Println(output) // print for visibility in test run

	if !strings.Contains(output, "ONLINE") {
		t.Errorf("Expected output to report ONLINE, got:\n%s", output)
	}
	if !strings.Contains(output, "enterprise") || !strings.Contains(output, "oss") {
		t.Errorf("Expected output to report editions, got:\n%s", output)
	}
	if !strings.Contains(output, "Telemetry Pipeline OK") {
		t.Errorf("Expected OTLP diagnostics to succeed, got:\n%s", output)
	}
}
