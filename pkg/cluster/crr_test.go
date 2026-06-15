package cluster

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"servstore/pkg/storage"
)

type mockCRRNode struct {
	store storage.StorageEngine
}

func (m *mockCRRNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	bucket, key := parts[0], parts[1]

	if r.Method == "GET" {
		reader, obj, err := m.store.GetObject(r.Context(), bucket, key, "")
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer reader.Close()
		w.Header().Set("Content-Type", obj.ContentType)
		w.Header().Set("ETag", obj.ETag)
		w.Header().Set("x-amz-version-id", obj.VersionID)
		if obj.Checksum != "" {
			w.Header().Set("x-amz-meta-blake3", obj.Checksum)
		}
		io.Copy(w, reader)
		return
	}

	if r.Method == "PUT" {
		size := r.ContentLength
		contentType := r.Header.Get("Content-Type")
		ctx := r.Context()
		if headerVer := r.Header.Get("X-ServStore-Version-Id"); headerVer != "" {
			ctx = context.WithValue(ctx, storage.VersionIDContextKey, headerVer)
		}
		_, err := m.store.PutObject(ctx, bucket, key, r.Body, size, contentType)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}

	if r.Method == "DELETE" {
		_, err := m.store.DeleteObject(r.Context(), bucket, key, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func TestCrossRegionReplication(t *testing.T) {
	// Setup region directories
	dir1, _ := os.MkdirTemp("", "servstore-crr-1-*")
	defer os.RemoveAll(dir1)
	dir2, _ := os.MkdirTemp("", "servstore-crr-2-*")
	defer os.RemoveAll(dir2)

	store1, _ := storage.NewLocalStore(dir1)
	defer store1.Close()
	store2, _ := storage.NewLocalStore(dir2)
	defer store2.Close()

	ctx := context.Background()
	_ = store1.CreateBucket(ctx, "crr-bucket")
	_ = store2.CreateBucket(ctx, "crr-bucket")

	// Host Node 2 (Region: eu-west-1)
	srv2 := httptest.NewServer(&mockCRRNode{store: store2})
	defer srv2.Close()

	addr2 := strings.TrimPrefix(srv2.URL, "http://")

	// Host Node 1 (Region: us-east-1)
	mm1 := NewMembershipManager("node-1", "localhost:8080", "").WithRegion("us-east-1")
	
	// Discover Node 2 (which is in eu-west-1)
	mm1.mu.Lock()
	mm1.peers["node-2"] = &NodeInfo{
		NodeID:   "node-2",
		Address:  addr2,
		Status:   "online",
		LastSeen: time.Now(),
		Region:   "eu-west-1",
	}
	mm1.mu.Unlock()

	// Since Node 2 is in eu-west-1, it should NOT be added to Node 1's local ring
	if mm1.ring.IsNodeInRing("node-2") {
		t.Error("expected node-2 in remote region to be excluded from local ring")
	}

	// Initialize CRR manager on Node 1
	crr1 := NewCRRManager(store1, mm1, "admin", "admin")
	crr1.Start(ctx)
	defer crr1.Stop()

	// Upload object in Region 1
	payload := []byte("cross region replication content")
	ver, err := store1.PutObject(ctx, "crr-bucket", "item-1", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put local object: %v", err)
	}

	// Manually trigger replication enqueue (in real Gateway this is handled in api.go handlePutObject)
	crr1.Enqueue(CRRJob{
		Bucket:    "crr-bucket",
		Key:       "item-1",
		VersionID: ver.VersionID,
		Delete:    false,
	})

	// Wait up to 5 seconds for async replication to complete
	deadline := time.Now().Add(5 * time.Second)
	replicated := false
	for time.Now().Before(deadline) {
		if _, err := store2.HeadObject(ctx, "crr-bucket", "item-1", ver.VersionID); err == nil {
			replicated = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !replicated {
		t.Fatal("timed out waiting for cross-region replication to complete")
	}

	// Read from Region 2 to verify payload match
	rc, _, err := store2.GetObject(ctx, "crr-bucket", "item-1", ver.VersionID)
	if err != nil {
		t.Fatalf("failed to read from region 2: %v", err)
	}
	readPayload, _ := io.ReadAll(rc)
	rc.Close()

	if !bytes.Equal(readPayload, payload) {
		t.Errorf("crr data mismatch: expected %q, got %q", payload, readPayload)
	}
}
