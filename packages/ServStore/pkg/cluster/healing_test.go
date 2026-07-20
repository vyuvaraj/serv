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

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

type mockS3Node struct {
	store storage.StorageEngine
}

func (m *mockS3Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	bucket, key := parts[0], parts[1]

	if r.Method == "HEAD" {
		_, err := m.store.HeadObject(r.Context(), bucket, key, "")
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	if r.Method == "PUT" {
		size := r.ContentLength
		_, err := m.store.PutObject(r.Context(), bucket, key, r.Body, size, "application/octet-stream")
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

func TestHealingManagerAutoHealingAndRebalancing(t *testing.T) {
	// Create two temp directories
	dir1, err := os.MkdirTemp("", "servstore-test-healer-1-*")
	if err != nil {
		t.Fatalf("failed to create temp dir 1: %v", err)
	}
	defer os.RemoveAll(dir1)

	dir2, err := os.MkdirTemp("", "servstore-test-healer-2-*")
	if err != nil {
		t.Fatalf("failed to create temp dir 2: %v", err)
	}
	defer os.RemoveAll(dir2)

	store1, err := storage.NewLocalStore(dir1)
	if err != nil {
		t.Fatalf("failed to initialize store 1: %v", err)
	}
	defer store1.Close()

	store2, err := storage.NewLocalStore(dir2)
	if err != nil {
		t.Fatalf("failed to initialize store 2: %v", err)
	}
	defer store2.Close()

	ctx := context.Background()
	_ = store1.CreateBucket(ctx, "test-bucket")
	_ = store2.CreateBucket(ctx, "test-bucket")

	// Set up node 2 mock server
	mockNode2 := &mockS3Node{store: store2}
	srv2 := httptest.NewServer(mockNode2)
	defer srv2.Close()

	// Parse host/port of target server
	addr2 := strings.TrimPrefix(srv2.URL, "http://")

	// Setup cluster membership manager on node 1
	mm := NewMembershipManager("node-1", "localhost:8080", addr2)
	mm.mu.Lock()
	mm.peers["node-2"] = &NodeInfo{
		NodeID:   "node-2",
		Address:  addr2,
		Status:   "online",
		LastSeen: time.Now(),
	}
	mm.ring.AddNode("node-2")
	mm.mu.Unlock()

	// Write object to store 1 locally
	data := []byte("healer-payload-data")
	_, err = store1.PutObject(ctx, "test-bucket", "heal-me", bytes.NewReader(data), int64(len(data)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	// Verify Node 2 doesn't have it yet
	_, err = store2.HeadObject(ctx, "test-bucket", "heal-me", "")
	if err == nil {
		t.Fatal("expected Node 2 to not have the object initially")
	}

	// Create HealingManager for node 1
	hm := NewHealingManager(store1, mm, 2, "admin", "admin")

	// Run healing cycle on Node 1
	err = hm.RunHealingCycle(ctx)
	if err != nil {
		t.Fatalf("RunHealingCycle failed: %v", err)
	}

	// Verify that the object was auto-healed (copied to Node 2)!
	_, err = store2.HeadObject(ctx, "test-bucket", "heal-me", "")
	if err != nil {
		t.Fatalf("expected Node 2 to have been healed, got error: %v", err)
	}

	// Read content from Node 2 to verify data integrity
	rc, _, err := store2.GetObject(ctx, "test-bucket", "heal-me", "")
	if err != nil {
		t.Fatalf("failed to read from Node 2: %v", err)
	}
	readData, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(readData, data) {
		t.Errorf("data mismatch: expected %q, got %q", data, readData)
	}

	// Test Rebalancing Handoff:
	mm.ring.RemoveNode("node-1")

	// Now Node 1 does not own the key. Healing cycle should handoff/purge it from Node 1.
	err = hm.RunHealingCycle(ctx)
	if err != nil {
		t.Fatalf("RunHealingCycle for rebalancing failed: %v", err)
	}

	// Verify Node 1 local storage is purged of the key
	_, err = store1.HeadObject(ctx, "test-bucket", "heal-me", "")
	if err == nil {
		t.Error("expected key to be purged from Node 1 local storage after rebalancing handoff")
	}
}
