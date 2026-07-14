package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

// buildTestWASM compiles a Go source string to a wasip1 binary
func buildTestWASM(t *testing.T, src string) []byte {
	t.Helper()
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "main.go")
	wasmPath := filepath.Join(tmpDir, "trigger.wasm")

	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", wasmPath, srcPath)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "unsupported GOOS") || strings.Contains(string(out), "unsupported") {
			t.Skipf("GOOS=wasip1 not supported by this Go toolchain: %s", out)
		}
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}
	return data
}

func TestS3Triggers(t *testing.T) {
	// A simple WASM code that parses stdin (the S3 notification event JSON)
	// and writes the bucket/key to stdout. We can verify that it executed.
	const wasmSrc = `package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type S3Event struct {
	Event     string ` + "`" + `json:"event"` + "`" + `
	Bucket    string ` + "`" + `json:"bucket"` + "`" + `
	Key       string ` + "`" + `json:"key"` + "`" + `
	Size      int64  ` + "`" + `json:"size"` + "`" + `
	ETag      string ` + "`" + `json:"etag"` + "`" + `
	Timestamp string ` + "`" + `json:"timestamp"` + "`" + `
}

func main() {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
		os.Exit(1)
	}

	var event S3Event
	if err := json.Unmarshal(body, &event); err != nil {
		fmt.Fprintf(os.Stderr, "failed to unmarshal: %v\n", err)
		os.Exit(1)
	}

	// Output structured result so parent test can verify
	fmt.Printf("TRIGGER_FIRED: event=%s bucket=%s key=%s\n", event.Event, event.Bucket, event.Key)
}
`
	wasmBytes := buildTestWASM(t, wasmSrc)

	// Set up local store
	dir, err := os.MkdirTemp("", "servstore-triggers-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	bucket := "trigger-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// Write the trigger WASM binary into the bucket
	_, err = store.PutObject(ctx, bucket, "triggers/log.wasm", bytes.NewReader(wasmBytes), int64(len(wasmBytes)), "application/wasm")
	if err != nil {
		t.Fatalf("failed to store wasm binary: %v", err)
	}

	// Setup Gateway
	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)
	server := httptest.NewServer(gateway)
	defer server.Close()

	client := &http.Client{}

	// 1. Configure WASM Trigger via PUT /<bucket>?triggers
	triggerConf := []storage.WASMTrigger{
		{
			Event:       "ObjectCreated:Put",
			WASMKey:     "triggers/log.wasm",
			Prefix:      "uploads/",
			Suffix:      ".txt",
			MemoryLimit: 64,
			Timeout:     5,
		},
	}
	confBytes, _ := json.Marshal(triggerConf)
	putConfReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"?triggers", bytes.NewReader(confBytes))
	putConfReq.Header.Set("Content-Type", "application/json")
	putConfReq.SetBasicAuth("admin", "admin")

	putConfResp, err := client.Do(putConfReq)
	if err != nil {
		t.Fatalf("PUT triggers request failed: %v", err)
	}
	defer putConfResp.Body.Close()
	if putConfResp.StatusCode != http.StatusOK {
		t.Fatalf("expected PUT triggers response 200, got %d", putConfResp.StatusCode)
	}

	// 2. Fetch config via GET /<bucket>?triggers and verify
	getConfReq, _ := http.NewRequest("GET", server.URL+"/"+bucket+"?triggers", nil)
	getConfReq.SetBasicAuth("admin", "admin")

	getConfResp, err := client.Do(getConfReq)
	if err != nil {
		t.Fatalf("GET triggers request failed: %v", err)
	}
	defer getConfResp.Body.Close()
	if getConfResp.StatusCode != http.StatusOK {
		t.Fatalf("expected GET triggers response 200, got %d", getConfResp.StatusCode)
	}

	var fetchedConf []storage.WASMTrigger
	if err := json.NewDecoder(getConfResp.Body).Decode(&fetchedConf); err != nil {
		t.Fatalf("failed to decode fetched triggers config: %v", err)
	}
	if len(fetchedConf) != 1 || fetchedConf[0].WASMKey != "triggers/log.wasm" {
		t.Fatalf("fetched configuration mismatch: %+v", fetchedConf)
	}

	// 3. Test matching triggers logic by writing an object.
	// Since WASM is executed asynchronously in a goroutine and logs success/failure,
	// we will intercept structured logs by replacing slog handler or wrapping it.
	// Let's capture slog output to find "WASM trigger executed successfully" or the custom output.
	// We can set a custom logger or read stdout/stderr from wasm.
	// Wait, api.go logs the output: slog.Info("WASM trigger executed successfully", "wasm_key", tr.WASMKey, "output", string(out))
	// We can hook a custom handler to slog or read logs.
	// Let's implement a simple memory-based slog handler.
	var logMu sync.Mutex
	var logs []string
	h := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			logMu.Lock()
			logs = append(logs, fmt.Sprintf("%s: %s", a.Key, a.Value.String()))
			logMu.Unlock()
			return a
		},
	})
	oldLogger := slog.Default()
	defer slog.SetDefault(oldLogger)
	slog.SetDefault(slog.New(h))

	// PUT request that matches the trigger (prefix "uploads/" and suffix ".txt")
	putMatchedReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/uploads/test.txt", strings.NewReader("hello test matched"))
	putMatchedReq.Header.Set("Content-Type", "text/plain")
	putMatchedReq.SetBasicAuth("admin", "admin")

	resp, err := client.Do(putMatchedReq)
	if err != nil {
		t.Fatalf("PUT matching object failed: %v", err)
	}
	resp.Body.Close()

	// Wait for the async goroutine execution (up to 5 seconds, polling)
	matchedFired := false
	var captured []string
	for i := 0; i < 100; i++ {
		logMu.Lock()
		captured = append([]string(nil), logs...)
		for _, l := range logs {
			if strings.Contains(l, "TRIGGER_FIRED") && strings.Contains(l, "key=uploads/test.txt") {
				matchedFired = true
				break
			}
		}
		logMu.Unlock()
		if matchedFired {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !matchedFired {
		t.Errorf("expected matching trigger to fire, captured logs: %v", captured)
	}

	// Reset logs list
	logMu.Lock()
	logs = nil
	logMu.Unlock()

	// PUT request that does NOT match the trigger (prefix does not match "uploads/")
	putUnmatchedReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/other/test.txt", strings.NewReader("hello test unmatched"))
	putUnmatchedReq.Header.Set("Content-Type", "text/plain")
	putUnmatchedReq.SetBasicAuth("admin", "admin")

	resp2, err := client.Do(putUnmatchedReq)
	if err != nil {
		t.Fatalf("PUT unmatched object failed: %v", err)
	}
	resp2.Body.Close()

	// Wait up to 1 second to see if unmatched triggers fire
	unmatchedFired := false
	for i := 0; i < 20; i++ {
		logMu.Lock()
		for _, l := range logs {
			if strings.Contains(l, "TRIGGER_FIRED") && strings.Contains(l, "key=other/test.txt") {
				unmatchedFired = true
				break
			}
		}
		logMu.Unlock()
		if unmatchedFired {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if unmatchedFired {
		logMu.Lock()
		capturedLogs := append([]string(nil), logs...)
		logMu.Unlock()
		t.Errorf("did not expect unmatched trigger to fire, captured logs: %v", capturedLogs)
	}
}
