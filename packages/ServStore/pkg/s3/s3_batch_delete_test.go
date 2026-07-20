package s3

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

func TestS3BatchDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := t.Context()
	bucket := "batch-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// Put test files
	_, _ = store.PutObject(ctx, bucket, "file1.txt", strings.NewReader("1"), 1, "text/plain")
	_, _ = store.PutObject(ctx, bucket, "file2.txt", strings.NewReader("2"), 1, "text/plain")
	_, _ = store.PutObject(ctx, bucket, "file3.txt", strings.NewReader("3"), 1, "text/plain")

	authProv := auth.NewAuthProvider("admin", "adminsecret", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)

	deleteReqXML := `<Delete>
		<Object><Key>file1.txt</Key></Object>
		<Object><Key>file2.txt</Key></Object>
	</Delete>`

	req := httptest.NewRequest("POST", "/batch-bucket?delete", strings.NewReader(deleteReqXML))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	var res DeleteResult
	if err := xml.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal delete result XML: %v", err)
	}

	if len(res.Deleted) != 2 {
		t.Errorf("expected 2 deleted results, got %d", len(res.Deleted))
	}

	// Verify files are indeed deleted from store
	rc1, _, err1 := store.GetObject(ctx, bucket, "file1.txt", "")
	if rc1 != nil {
		rc1.Close()
	}
	rc2, _, err2 := store.GetObject(ctx, bucket, "file2.txt", "")
	if rc2 != nil {
		rc2.Close()
	}
	rc3, _, err3 := store.GetObject(ctx, bucket, "file3.txt", "")
	if rc3 != nil {
		rc3.Close()
	}

	if err1 == nil {
		t.Errorf("expected file1.txt to be deleted")
	}
	if err2 == nil {
		t.Errorf("expected file2.txt to be deleted")
	}
	if err3 != nil {
		t.Errorf("expected file3.txt to still exist, got err: %v", err3)
	}

	// Allow background asynchronous access log writing goroutines to finish
	time.Sleep(100 * time.Millisecond)
}
