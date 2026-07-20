package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestBackupRestoreAPI(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	// Auth provider with validation disabled for simplicity
	authProv := auth.NewAuthProvider("", "", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)

	ctx := context.Background()
	bucket := "api-restore-bucket"

	// Create bucket
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("CreateBucket failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Write version 1
	c1 := []byte("original-content")
	_, err = store.PutObject(ctx, bucket, "data", bytes.NewReader(c1), int64(len(c1)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	t1 := time.Now()
	time.Sleep(100 * time.Millisecond)

	// Overwrite with version 2
	c2 := []byte("overwritten-content")
	_, err = store.PutObject(ctx, bucket, "data", bytes.NewReader(c2), int64(len(c2)), "text/plain")
	if err != nil {
		t.Fatalf("Overwrite failed: %v", err)
	}

	// Verify current content is version 2
	rc, _, err := store.GetObject(ctx, bucket, "data", "")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	d, _ := io.ReadAll(rc)
	rc.Close()
	if string(d) != "overwritten-content" {
		t.Fatalf("Expected overwritten-content, got %q", string(d))
	}

	// Call POST /admin/backup/restore?bucket=api-restore-bucket&time=t1
	timeStr := url.QueryEscape(t1.Format(time.RFC3339Nano))
	req := httptest.NewRequest("POST", fmt.Sprintf("/admin/backup/restore?bucket=%s&time=%s", bucket, timeStr), nil)
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Verify content is restored to version 1
	rc2, _, err := store.GetObject(ctx, bucket, "data", "")
	if err != nil {
		t.Fatalf("GetObject failed after restore: %v", err)
	}
	d2, _ := io.ReadAll(rc2)
	rc2.Close()
	if string(d2) != "original-content" {
		t.Errorf("Expected original-content, got %q", string(d2))
	}
}
