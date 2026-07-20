package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestS3Features(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "servstore-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := storage.NewLocalStore(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	authProv := auth.NewAuthProvider("admin", "adminsecret", false) // disable auth for easy testing
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 1, 1)

	// Create bucket b1
	ctx := context.Background()
	if err := store.CreateBucket(ctx, "b1"); err != nil {
		t.Fatalf("Failed to create bucket b1: %v", err)
	}
	// Create bucket b2
	if err := store.CreateBucket(ctx, "b2"); err != nil {
		t.Fatalf("Failed to create bucket b2: %v", err)
	}

	// Put an object in b1
	_, err = store.PutObject(ctx, "b1", "file1.txt", strings.NewReader("hello file1"), 11, "text/plain")
	if err != nil {
		t.Fatalf("Failed to put file1: %v", err)
	}

	// Put another object in b1
	_, err = store.PutObject(ctx, "b1", "file2.txt", strings.NewReader("hello file2"), 11, "text/plain")
	if err != nil {
		t.Fatalf("Failed to put file2: %v", err)
	}

	// 1. Test CopyObject: copy b1/file1.txt to b2/copied.txt
	req := httptest.NewRequest("PUT", "/b2/copied.txt", nil)
	req.Header.Set("x-amz-copy-source", "/b1/file1.txt")
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("CopyObject: expected 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Verify copied file content
	reader, obj, err := store.GetObject(ctx, "b2", "copied.txt", "")
	if err != nil {
		t.Fatalf("Failed to get copied object: %v", err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)
	reader.Close()
	if buf.String() != "hello file1" {
		t.Errorf("CopyObject: expected content 'hello file1', got %q", buf.String())
	}
	if obj.ContentType != "text/plain" {
		t.Errorf("CopyObject: expected content-type 'text/plain', got %q", obj.ContentType)
	}

	// 2. Test Object Tagging
	taggingXML := `<Tagging><TagSet><Tag><Key>env</Key><Value>prod</Value></Tag><Tag><Key>project</Key><Value>serv</Value></Tag></TagSet></Tagging>`
	req = httptest.NewRequest("PUT", "/b1/file1.txt?tagging", strings.NewReader(taggingXML))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("PutObjectTagging: expected 200, got %d", w.Code)
	}

	// Get Tags
	req = httptest.NewRequest("GET", "/b1/file1.txt?tagging", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GetObjectTagging: expected 200, got %d", w.Code)
	}
	var tagsRes Tagging
	if err := xml.NewDecoder(w.Body).Decode(&tagsRes); err != nil {
		t.Fatalf("Failed to decode tagging XML: %v", err)
	}
	if len(tagsRes.TagSet) != 2 {
		t.Errorf("GetObjectTagging: expected 2 tags, got %d", len(tagsRes.TagSet))
	}
	tagsMap := make(map[string]string)
	for _, t := range tagsRes.TagSet {
		tagsMap[t.Key] = t.Value
	}
	if tagsMap["env"] != "prod" || tagsMap["project"] != "github.com/vyuvaraj/serv/packages/Serv-lang" {
		t.Errorf("GetObjectTagging: expected tags env=prod and project=serv, got: %v", tagsMap)
	}

	// List objects with tag-filter
	req = httptest.NewRequest("GET", "/b1?tag-filter=env:prod", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("ListObjects with tag-filter: expected 200, got %d", w.Code)
	}
	var listRes ListBucketResult
	if err := xml.NewDecoder(w.Body).Decode(&listRes); err != nil {
		t.Fatalf("Failed to decode ListObjects XML: %v", err)
	}
	// Only file1.txt has tag env:prod, file2.txt does not
	if len(listRes.Contents) != 1 {
		t.Errorf("ListObjects with tag-filter: expected 1 object, got %d", len(listRes.Contents))
	} else if listRes.Contents[0].Key != "file1.txt" {
		t.Errorf("ListObjects with tag-filter: expected key 'file1.txt', got %q", listRes.Contents[0].Key)
	}

	// Delete Tags
	req = httptest.NewRequest("DELETE", "/b1/file1.txt?tagging", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("DeleteObjectTagging: expected 204, got %d", w.Code)
	}

	// Verify tags are gone
	req = httptest.NewRequest("GET", "/b1/file1.txt?tagging", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GetObjectTagging after delete: expected 200, got %d", w.Code)
	}
	tagsRes = Tagging{}
	xml.NewDecoder(w.Body).Decode(&tagsRes)
	if len(tagsRes.TagSet) != 0 {
		t.Errorf("GetObjectTagging after delete: expected 0 tags, got %d", len(tagsRes.TagSet))
	}

	// 3. Test Batch Delete API
	deleteXML := `<Delete><Quiet>false</Quiet><Object><Key>file1.txt</Key></Object><Object><Key>file2.txt</Key></Object></Delete>`
	req = httptest.NewRequest("POST", "/b1?delete", strings.NewReader(deleteXML))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("BatchDelete: expected 200, got %d. Body: %s", w.Code, w.Body.String())
	}
	var deleteRes DeleteResult
	if err := xml.NewDecoder(w.Body).Decode(&deleteRes); err != nil {
		t.Fatalf("Failed to decode delete result XML: %v", err)
	}
	if len(deleteRes.Deleted) != 2 {
		t.Errorf("BatchDelete: expected 2 deleted entries, got %d", len(deleteRes.Deleted))
	}
	if len(deleteRes.Errors) != 0 {
		t.Errorf("BatchDelete: expected 0 errors, got %d", len(deleteRes.Errors))
	}

	// Verify they are deleted in storage
	_, _, err = store.GetObject(ctx, "b1", "file1.txt", "")
	if err == nil {
		t.Error("file1.txt should have been deleted")
	}
	_, _, err = store.GetObject(ctx, "b1", "file2.txt", "")
	if err == nil {
		t.Error("file2.txt should have been deleted")
	}
}
