package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWALRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "servstore_wal_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize store
	store, err := NewLocalStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}

	// 1. Create a bucket via standard endpoint
	err = store.CreateBucket(context.Background(), "my-bucket")
	if err != nil {
		t.Fatalf("CreateBucket failed: %v", err)
	}

	// 2. Put an object (which will write to WAL)
	content := []byte("hello wal durability")
	_, err = store.PutObject(context.Background(), "my-bucket", "test.txt", bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}
	store.Close()

	// Clean database and metadata directory to simulate server crash (retaining only backup.wal)
	dbPath := filepath.Join(tmpDir, ".metadata_db")
	_ = os.RemoveAll(dbPath)
	_ = os.RemoveAll(filepath.Join(tmpDir, "my-bucket"))

	// Initialize new store instance on same directory — it should automatically trigger RecoverFromWAL
	newStore, err := NewLocalStore(tmpDir)
	if err != nil {
		t.Fatalf("failed to instantiate store during recovery: %v", err)
	}
	defer newStore.Close()

	// Verify bucket metadata was reconstructed
	b, err := newStore.GetBucket(context.Background(), "my-bucket")
	if err != nil {
		t.Errorf("expected recovered bucket: %v", err)
	}
	if b.Name != "my-bucket" {
		t.Errorf("expected bucket name 'my-bucket', got '%s'", b.Name)
	}
}
