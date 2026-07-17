package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

func TestBucketConversationalQuery(t *testing.T) {
	// 1. Setup local storage engine & auth provider
	store, err := storage.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer func() {
		store.Close()
		// Force GC and a micro-sleep to ensure all Windows file handles from Pebble & zstd are fully released
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
	}()

	authProvider := auth.NewAuthProvider("", "", false) // disabled auth for easy testing

	// 2. Setup S3 Gateway
	gateway := NewGateway(store, authProvider, nil, nil, 1, false, 1, 1)

	// 3. Create bucket and seed a text document
	ctx := context.Background()
	bucket := "doc-bucket"
	err = store.CreateBucket(ctx, bucket)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	docContent := "Authentication is managed via secure tokens and sessions."
	_, err = store.PutObject(ctx, bucket, "auth_docs.txt", bytes.NewReader([]byte(docContent)), int64(len(docContent)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put object: %v", err)
	}

	// 4. Perform conversational RAG query
	req := httptest.NewRequest("GET", "/doc-bucket?ask=How+is+authentication+managed?", nil)
	w := httptest.NewRecorder()

	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal JSON response: %v", err)
	}

	if resp["bucket"] != "doc-bucket" {
		t.Errorf("expected bucket 'doc-bucket', got '%v'", resp["bucket"])
	}

	answer, ok := resp["answer"].(string)
	if !ok || answer == "" {
		t.Errorf("expected synthesized answer in response, got: %v", resp)
	}
	if !strings.Contains(strings.ToLower(answer), "token") {
		t.Errorf("expected answer to refer to 'token' based on seeded doc, got: %s", answer)
	}
}
