package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIncrementalBackupAndPITR(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-backup-test-*")
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
	bucketName := "pitr-bucket"

	// 1. Create Bucket
	err = store.CreateBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("CreateBucket failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// 2. Put Version 1 of object 1
	c1 := []byte("object-1-v1-data")
	_, err = store.PutObject(ctx, bucketName, "obj1", bytes.NewReader(c1), int64(len(c1)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject v1 failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	t1 := time.Now()
	time.Sleep(100 * time.Millisecond)

	// 3. Put Version 2 of object 1 and object 2
	c2 := []byte("object-1-v2-data")
	_, err = store.PutObject(ctx, bucketName, "obj1", bytes.NewReader(c2), int64(len(c2)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject v2 failed: %v", err)
	}

	c3 := []byte("object-2-data")
	_, err = store.PutObject(ctx, bucketName, "obj2", bytes.NewReader(c3), int64(len(c3)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject obj2 failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	t2 := time.Now()
	time.Sleep(100 * time.Millisecond)

	// 4. Delete both objects
	_, err = store.DeleteObject(ctx, bucketName, "obj1", "")
	if err != nil {
		t.Fatalf("DeleteObject obj1 failed: %v", err)
	}
	_, err = store.DeleteObject(ctx, bucketName, "obj2", "")
	if err != nil {
		t.Fatalf("DeleteObject obj2 failed: %v", err)
	}

	// Verify they are delete-marked/missing
	_, _, err = store.GetObject(ctx, bucketName, "obj1", "")
	if err == nil {
		t.Errorf("Expected obj1 to be deleted")
	}

	// 5. Restore to T2 (both should exist, obj1 should be v2)
	err = store.RestoreBucketToPointInTime(ctx, bucketName, t2)
	if err != nil {
		t.Fatalf("Restore to T2 failed: %v", err)
	}

	rc, _, err := store.GetObject(ctx, bucketName, "obj1", "")
	if err != nil {
		t.Fatalf("Failed to get obj1 after restore to T2: %v", err)
	}
	d, _ := io.ReadAll(rc)
	rc.Close()
	if string(d) != "object-1-v2-data" {
		t.Errorf("Expected 'object-1-v2-data', got %q", string(d))
	}

	rc2, _, err := store.GetObject(ctx, bucketName, "obj2", "")
	if err != nil {
		t.Fatalf("Failed to get obj2 after restore to T2: %v", err)
	}
	d2, _ := io.ReadAll(rc2)
	rc2.Close()
	if string(d2) != "object-2-data" {
		t.Errorf("Expected 'object-2-data', got %q", string(d2))
	}

	// 6. Restore to T1 (only obj1 should exist, and it should be v1)
	err = store.RestoreBucketToPointInTime(ctx, bucketName, t1)
	if err != nil {
		t.Fatalf("Restore to T1 failed: %v", err)
	}

	rc, _, err = store.GetObject(ctx, bucketName, "obj1", "")
	if err != nil {
		t.Fatalf("Failed to get obj1 after restore to T1: %v", err)
	}
	d, _ = io.ReadAll(rc)
	rc.Close()
	if string(d) != "object-1-v1-data" {
		t.Errorf("Expected 'object-1-v1-data', got %q", string(d))
	}

	_, _, err = store.GetObject(ctx, bucketName, "obj2", "")
	if err == nil {
		t.Errorf("Expected obj2 to not exist at T1")
	}

	// Verify backup.wal file was created
	walPath := filepath.Join(tempDir, "backup.wal")
	if _, err := os.Stat(walPath); os.IsNotExist(err) {
		t.Errorf("Expected backup.wal to be created in store root")
	}
}
