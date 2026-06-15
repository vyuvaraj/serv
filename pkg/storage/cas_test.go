package storage

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestContentAddressedStorage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-cas-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewLocalStore(tempDir)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	bucketName := "cas-bucket"
	err = store.CreateBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// 1. Enable ContentAddressable mode
	err = store.SetBucketContentAddressable(ctx, bucketName, true)
	if err != nil {
		t.Fatalf("failed to enable CAS mode: %v", err)
	}

	// Double check metadata flag is set
	bMeta, err := store.GetBucket(ctx, bucketName)
	if err != nil || !bMeta.ContentAddressable {
		t.Fatalf("expected ContentAddressable to be true, got %v", bMeta.ContentAddressable)
	}

	content := []byte("unique deduplicated block of data")

	// 2. Put two different objects with the exact same content
	ver1, err := store.PutObject(ctx, bucketName, "key-1", bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put key-1: %v", err)
	}

	ver2, err := store.PutObject(ctx, bucketName, "key-2", bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put key-2: %v", err)
	}

	// Both version IDs should be identical and equal to "cas-<blake3_hash>"
	if ver1.VersionID != ver2.VersionID {
		t.Errorf("expected identical version IDs for identical content, got %s and %s", ver1.VersionID, ver2.VersionID)
	}
	if !strings.HasPrefix(ver1.VersionID, "cas-") {
		t.Errorf("expected version ID prefix 'cas-', got %s", ver1.VersionID)
	}

	// 3. Verify they point to the exact same file path on disk (deduplication!)
	path1 := store.getObjectDataPath(bucketName, "key-1", ver1.VersionID)
	path2 := store.getObjectDataPath(bucketName, "key-2", ver2.VersionID)

	if path1 != path2 {
		t.Errorf("expected identical data paths, got %q and %q", path1, path2)
	}

	// 4. Verify garbage collection / reference counting on deletion
	// Delete key-1. Since key-2 still references the CAS file, the data block must NOT be removed from disk.
	_, err = store.DeleteObject(ctx, bucketName, "key-1", ver1.VersionID)
	if err != nil {
		t.Fatalf("failed to delete key-1 version: %v", err)
	}

	if _, err := os.Stat(path1); os.IsNotExist(err) {
		t.Fatal("expected data file to remain on disk because key-2 still holds a reference, but it was deleted")
	}

	// Delete key-2 as well. Since there are no more references to the CAS block, it should be deleted from disk.
	_, err = store.DeleteObject(ctx, bucketName, "key-2", ver2.VersionID)
	if err != nil {
		t.Fatalf("failed to delete key-2 version: %v", err)
	}

	if _, err := os.Stat(path2); !os.IsNotExist(err) {
		t.Error("expected data file to be permanently deleted from disk since all references are gone, but it still exists")
	}
}
