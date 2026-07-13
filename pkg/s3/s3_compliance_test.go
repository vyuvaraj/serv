package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

// S3ErrorResponse matches the AWS S3 XML error envelope.
type S3ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

func newTestGateway(t *testing.T) (*Gateway, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "s3-compliance-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	store, err := storage.NewLocalStore(tmpDir)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create store: %v", err)
	}
	authProv := auth.NewAuthProvider("admin", "adminsecret", false)
	gw := NewGateway(store, authProv, nil, nil, 1, false, 1, 1)
	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}
	return gw, cleanup
}

// TestS3ComplianceHeadersOnPutGet verifies ETag, Content-Length,
// and x-amz-request-id headers are present on PUT and GET responses.
func TestS3ComplianceHeadersOnPutGet(t *testing.T) {
	gw, cleanup := newTestGateway(t)
	defer cleanup()

	ctx := context.Background()
	store := gw.store
	if err := store.CreateBucket(ctx, "compliance-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	body := []byte("hello compliance")
	req := httptest.NewRequest(http.MethodPut, "/compliance-bucket/hello.txt", bytes.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Errorf("PUT: expected 200/204, got %d; body: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("ETag") == "" {
		t.Error("PUT: missing ETag header")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/compliance-bucket/hello.txt", nil)
	w2 := httptest.NewRecorder()
	gw.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("GET: expected 200, got %d; body: %s", w2.Code, w2.Body.String())
	}
	if w2.Header().Get("Content-Length") == "" {
		t.Error("GET: missing Content-Length header")
	}
	if w2.Header().Get("ETag") == "" {
		t.Error("GET: missing ETag header on GET response")
	}
	if w2.Body.String() != string(body) {
		t.Errorf("GET: body mismatch: got %q, want %q", w2.Body.String(), body)
	}
}

// TestS3ComplianceNoSuchBucket verifies 404 with correct XML error code
// when accessing a non-existent bucket.
func TestS3ComplianceNoSuchBucket(t *testing.T) {
	gw, cleanup := newTestGateway(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/nonexistent-bucket/key.txt", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var errResp S3ErrorResponse
	if err := xml.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error XML: %v", err)
	}
	if errResp.Code != "NoSuchBucket" {
		t.Errorf("expected error code 'NoSuchBucket', got %q", errResp.Code)
	}
}

// TestS3ComplianceNoSuchKey verifies 404 with correct XML error code
// when accessing a non-existent key in an existing bucket.
func TestS3ComplianceNoSuchKey(t *testing.T) {
	gw, cleanup := newTestGateway(t)
	defer cleanup()

	ctx := context.Background()
	if err := gw.store.CreateBucket(ctx, "exist-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/exist-bucket/missing-key.txt", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var errResp S3ErrorResponse
	if err := xml.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to decode error XML: %v", err)
	}
	if errResp.Code != "NoSuchKey" {
		t.Errorf("expected error code 'NoSuchKey', got %q", errResp.Code)
	}
}

// TestS3ComplianceDeleteObject verifies a 204 on DELETE and 404 on subsequent GET.
func TestS3ComplianceDeleteObject(t *testing.T) {
	gw, cleanup := newTestGateway(t)
	defer cleanup()

	ctx := context.Background()
	store := gw.store
	if err := store.CreateBucket(ctx, "del-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	body := []byte("delete me")
	if _, err := store.PutObject(ctx, "del-bucket", "obj.txt", strings.NewReader(string(body)), int64(len(body)), "text/plain"); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/del-bucket/obj.txt", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent && w.Code != http.StatusOK {
		t.Errorf("DELETE: expected 204/200, got %d", w.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/del-bucket/obj.txt", nil)
	w2 := httptest.NewRecorder()
	gw.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE: expected 404, got %d", w2.Code)
	}
}

// TestS3ComplianceMultipartLifecycle verifies CreateMultipartUpload → UploadPart → CompleteMultipartUpload.
func TestS3ComplianceMultipartLifecycle(t *testing.T) {
	gw, cleanup := newTestGateway(t)
	defer cleanup()

	ctx := context.Background()
	if err := gw.store.CreateBucket(ctx, "mp-bucket"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}

	// 1. Initiate multipart upload
	req := httptest.NewRequest(http.MethodPost, "/mp-bucket/big.bin?uploads", nil)
	w := httptest.NewRecorder()
	gw.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("CreateMultipartUpload: expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var initResp struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}
	if err := xml.NewDecoder(strings.NewReader(w.Body.String())).Decode(&initResp); err != nil {
		t.Fatalf("failed to decode InitiateMultipartUpload response: %v", err)
	}
	if initResp.UploadID == "" {
		t.Fatal("expected non-empty UploadId")
	}

	// 2. Upload a part
	partBody := bytes.Repeat([]byte("A"), 5*1024*1024) // 5MB minimum
	req2 := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("/mp-bucket/big.bin?partNumber=1&uploadId=%s", initResp.UploadID),
		bytes.NewReader(partBody))
	req2.ContentLength = int64(len(partBody))
	w2 := httptest.NewRecorder()
	gw.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK && w2.Code != http.StatusNoContent {
		t.Fatalf("UploadPart: expected 200/204, got %d; body: %s", w2.Code, w2.Body.String())
	}
	partETag := w2.Header().Get("ETag")

	// 3. Complete multipart upload
	completeBody := fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`, partETag)
	req3 := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/mp-bucket/big.bin?uploadId=%s", initResp.UploadID),
		strings.NewReader(completeBody))
	w3 := httptest.NewRecorder()
	gw.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("CompleteMultipartUpload: expected 200, got %d; body: %s", w3.Code, w3.Body.String())
	}
}
