package s3

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestS3MockMode(t *testing.T) {
	// Create S3 Gateway in mock mode
	authProv := auth.NewAuthProvider("admin", "adminsecret", false)
	gateway := NewGateway(nil, authProv, nil, nil, 1, false, 1, 1).WithMock(true)

	// 1. Test GET / (List All Buckets)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("ListBuckets mock: expected 200, got %d", w.Code)
	}

	var bucketsRes ListAllMyBucketsResult
	if err := xml.NewDecoder(w.Body).Decode(&bucketsRes); err != nil {
		t.Fatalf("Failed to decode ListBuckets XML: %v", err)
	}
	if len(bucketsRes.Buckets) != 2 || bucketsRes.Buckets[0].Name != "mock-bucket-1" {
		t.Errorf("Unexpected buckets: %+v", bucketsRes.Buckets)
	}

	// 2. Test PUT /b1 (Create Bucket)
	req = httptest.NewRequest("PUT", "/b1", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("CreateBucket mock: expected 200, got %d", w.Code)
	}

	// 3. Test GET /b1 (List Objects)
	req = httptest.NewRequest("GET", "/b1", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("ListObjects mock: expected 200, got %d", w.Code)
	}
	var objectsRes ListBucketResult
	if err := xml.NewDecoder(w.Body).Decode(&objectsRes); err != nil {
		t.Fatalf("Failed to decode ListObjects XML: %v", err)
	}
	if len(objectsRes.Contents) != 1 || objectsRes.Contents[0].Key != "mock-object-1.txt" {
		t.Errorf("Unexpected objects: %+v", objectsRes.Contents)
	}

	// 4. Test PUT /b1/file.txt (Put Object)
	req = httptest.NewRequest("PUT", "/b1/file.txt", strings.NewReader("some data"))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("PutObject mock: expected 200, got %d", w.Code)
	}
	if w.Header().Get("ETag") != `"mock-etag"` {
		t.Errorf("Expected mock ETag, got %q", w.Header().Get("ETag"))
	}

	// 5. Test GET /b1/file.txt (Get Object)
	req = httptest.NewRequest("GET", "/b1/file.txt", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GetObject mock: expected 200, got %d", w.Code)
	}
	if w.Body.String() != "mock-s3-data" {
		t.Errorf("Expected content 'mock-s3-data', got %q", w.Body.String())
	}
}

func TestS3ActiveActiveConflictResolution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "s3-lww-test-*")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	localStore, err := storage.NewLocalStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}

	authProv := auth.NewAuthProvider("admin", "adminsecret", false)
	gateway := NewGateway(localStore, authProv, nil, nil, 1, false, 1, 1)

	ctx := context.Background()
	_ = localStore.CreateBucket(ctx, "my-bucket")

	// 1. Put newer local object
	obj, err := localStore.PutObject(ctx, "my-bucket", "key.txt", strings.NewReader("local newer data"), 16, "text/plain")
	if err != nil {
		t.Fatalf("failed to put object: %v", err)
	}

	// 2. Make replication request with older timestamp header
	req := httptest.NewRequest("PUT", "/my-bucket/key.txt", strings.NewReader("remote older data"))
	req.Header.Set("X-ServStore-Replicated", "true")
	req.Header.Set("X-ServStore-Timestamp", obj.LastModified.Add(-10 * time.Second).Format(time.RFC3339))
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	// Read object back, verify conflict resolution retained local newer data
	r, _, err := localStore.GetObject(ctx, "my-bucket", "key.txt", "")
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}
	defer r.Close()
	data, _ := io.ReadAll(r)
	if string(data) != "local newer data" {
		t.Errorf("Expected 'local newer data' (LWW), got %q", string(data))
	}
}
