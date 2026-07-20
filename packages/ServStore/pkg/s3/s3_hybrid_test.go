package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestS3HybridAndExtensions(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "servstore-hybrid-test-*")
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

	ctx := context.Background()

	// ==========================================
	// 1. ZSTD Compression Test
	// ==========================================
	bucketComp := "b-comp"
	if err := store.CreateBucket(ctx, bucketComp); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Put compressible text object
	compText := "This is a highly compressible text file. " + strings.Repeat("hello world ", 50)
	req := httptest.NewRequest("PUT", "/b-comp/text.txt", strings.NewReader(compText))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT compressible failed: %d", w.Code)
	}

	// Verify it was compressed on disk
	ov, err := store.HeadObject(ctx, bucketComp, "text.txt", "")
	if err != nil {
		t.Fatalf("Head object failed: %v", err)
	}
	if !ov.Compressed {
		t.Errorf("expected object to be compressed, got false")
	}

	// GET the compressible object and verify transparent decompression
	req = httptest.NewRequest("GET", "/b-comp/text.txt", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET compressible failed: %d", w.Code)
	}
	if w.Body.String() != compText {
		t.Errorf("expected content %q, got %q", compText, w.Body.String())
	}

	// Put incompressible binary object
	incompBytes := []byte{0x00, 0x01, 0x02, 0x03, 0x04}
	req = httptest.NewRequest("PUT", "/b-comp/binary.bin", bytes.NewReader(incompBytes))
	req.Header.Set("Content-Type", "application/octet-stream")
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT incompressible failed: %d", w.Code)
	}

	// Verify it was NOT compressed on disk
	ovIncomp, err := store.HeadObject(ctx, bucketComp, "binary.bin", "")
	if err != nil {
		t.Fatalf("Head object failed: %v", err)
	}
	if ovIncomp.Compressed {
		t.Errorf("expected object not to be compressed, got true")
	}

	// ==========================================
	// 2. Bucket Quota Test
	// ==========================================
	bucketQuota := "b-quota"
	if err := store.CreateBucket(ctx, bucketQuota); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Get initial quota (should be 0)
	req = httptest.NewRequest("GET", "/b-quota?quota", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET quota failed: %d", w.Code)
	}
	var quotaRes map[string]int64
	if err := json.Unmarshal(w.Body.Bytes(), &quotaRes); err != nil {
		t.Fatalf("Failed to parse quota JSON: %v", err)
	}
	if quotaRes["quota"] != 0 {
		t.Errorf("expected default quota 0, got %d", quotaRes["quota"])
	}

	// Set quota to 20 bytes
	req = httptest.NewRequest("PUT", "/b-quota?quota=20", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT quota=20 failed: %d", w.Code)
	}

	// Verify quota is set to 20
	req = httptest.NewRequest("GET", "/b-quota?quota", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET quota failed: %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &quotaRes); err != nil {
		t.Fatalf("Failed to parse quota JSON: %v", err)
	}
	if quotaRes["quota"] != 20 {
		t.Errorf("expected quota 20, got %d", quotaRes["quota"])
	}

	// Put 15-byte object (should succeed)
	req = httptest.NewRequest("PUT", "/b-quota/obj1", strings.NewReader("123456789012345"))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("PUT obj1 expected 200, got %d", w.Code)
	}

	// Put 10-byte object (should fail, total would be 25 > 20)
	req = httptest.NewRequest("PUT", "/b-quota/obj2", strings.NewReader("1234567890"))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("PUT obj2 expected 409 Conflict, got %d. Body: %s", w.Code, w.Body.String())
	}

	// Overwrite obj1 with 5-byte object (should succeed, versioning disabled so old is replaced)
	req = httptest.NewRequest("PUT", "/b-quota/obj1", strings.NewReader("abcde"))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("PUT obj1 overwrite expected 200, got %d", w.Code)
	}

	// Now try putting 10-byte object again (5 + 10 = 15 <= 20, should succeed)
	req = httptest.NewRequest("PUT", "/b-quota/obj2", strings.NewReader("1234567890"))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("PUT obj2 expected 200, got %d", w.Code)
	}

	// ==========================================
	// 3. Vector Similarity + Metadata Hybrid Queries Test
	// ==========================================
	bucketSearch := "b-search"
	if err := store.CreateBucket(ctx, bucketSearch); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Put 3 text objects with semantic content
	doc1 := "Distributed storage engines use consensus algorithms like Raft to replicate metadata consistently."
	doc2 := "Baking bread requires flour, water, yeast, and salt mixed and baked in a hot oven."
	doc3 := "Machine learning systems generate embeddings to compute cosine similarity."

	_, _ = store.PutObject(ctx, bucketSearch, "raft-doc.txt", strings.NewReader(doc1), int64(len(doc1)), "text/plain")
	_, _ = store.PutObject(ctx, bucketSearch, "recipe.txt", strings.NewReader(doc2), int64(len(doc2)), "text/plain")
	_, _ = store.PutObject(ctx, bucketSearch, "ml-embeddings.txt", strings.NewReader(doc3), int64(len(doc3)), "text/plain")

	// Add tags without xmlns attribute
	taggingXML1 := `<Tagging><TagSet><Tag><Key>category</Key><Value>tech</Value></Tag></TagSet></Tagging>`
	req = httptest.NewRequest("PUT", "/b-search/raft-doc.txt?tagging", strings.NewReader(taggingXML1))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	fmt.Printf("Tagging raft-doc.txt status: %d, body: %s\n", w.Code, w.Body.String())

	taggingXML2 := `<Tagging><TagSet><Tag><Key>category</Key><Value>food</Value></Tag></TagSet></Tagging>`
	req = httptest.NewRequest("PUT", "/b-search/recipe.txt?tagging", strings.NewReader(taggingXML2))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	taggingXML3 := `<Tagging><TagSet><Tag><Key>category</Key><Value>tech</Value></Tag></TagSet></Tagging>`
	req = httptest.NewRequest("PUT", "/b-search/ml-embeddings.txt?tagging", strings.NewReader(taggingXML3))
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	// Print tags directly from store to see if they got set
	t1, _ := store.GetObjectTagging(ctx, bucketSearch, "raft-doc.txt", "")
	fmt.Printf("raft-doc.txt tags in store: %v\n", t1)

	// Test 3.1: Basic Semantic Search
	req = httptest.NewRequest("GET", "/b-search?query=semantic&q=consensus+algorithms", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Semantic Search GET failed: %d", w.Code)
	}
	var res1 ListBucketResult
	if err := xml.NewDecoder(w.Body).Decode(&res1); err != nil {
		t.Fatalf("Failed to decode ListBucketResult: %v", err)
	}
	if len(res1.Contents) == 0 {
		t.Fatalf("expected search results, got 0")
	}
	if res1.Contents[0].Key != "raft-doc.txt" {
		t.Errorf("expected top match to be 'raft-doc.txt', got %s", res1.Contents[0].Key)
	}

	// Test 3.2: Hybrid Search (Semantic + Tag Filter)
	req = httptest.NewRequest("GET", "/b-search?query=semantic&q=consensus+algorithms&filter=category:food", nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Hybrid Search GET failed: %d", w.Code)
	}
	var res2 ListBucketResult
	if err := xml.NewDecoder(w.Body).Decode(&res2); err != nil {
		t.Fatalf("Failed to decode ListBucketResult: %v", err)
	}
	fmt.Printf("Hybrid search category:food result: %+v\n", res2.Contents)
	if len(res2.Contents) == 0 {
		t.Fatalf("expected search results for category:food, got 0")
	}
	for _, c := range res2.Contents {
		if c.Key == "raft-doc.txt" {
			t.Errorf("did not expect 'raft-doc.txt' in results when category:food was filtered")
		}
	}

	// Test 3.3: Hybrid Search (Semantic + Time Filter - after)
	yesterday := time.Now().Add(-24 * time.Hour).Format("2006-01-02")
	req = httptest.NewRequest("GET", "/b-search?query=semantic&q=consensus+algorithms&after="+yesterday, nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Hybrid Search with after filter GET failed: %d", w.Code)
	}
	var res3 ListBucketResult
	if err := xml.NewDecoder(w.Body).Decode(&res3); err != nil {
		t.Fatalf("Failed to decode ListBucketResult: %v", err)
	}
	foundRaft := false
	for _, c := range res3.Contents {
		if c.Key == "raft-doc.txt" {
			foundRaft = true
		}
	}
	if !foundRaft {
		t.Errorf("expected 'raft-doc.txt' in results when after filter is in the past")
	}

	// Test 3.4: Hybrid Search (Semantic + Time Filter - before)
	req = httptest.NewRequest("GET", "/b-search?query=semantic&q=consensus+algorithms&before="+yesterday, nil)
	w = httptest.NewRecorder()
	gateway.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Hybrid Search with before filter GET failed: %d", w.Code)
	}
	var res4 ListBucketResult
	if err := xml.NewDecoder(w.Body).Decode(&res4); err != nil {
		t.Fatalf("Failed to decode ListBucketResult: %v", err)
	}
	fmt.Printf("Hybrid search before yesterday result: %+v\n", res4.Contents)
	for _, c := range res4.Contents {
		if c.Key == "raft-doc.txt" {
			t.Errorf("did not expect 'raft-doc.txt' in results when before filter is in the past")
		}
	}
}
