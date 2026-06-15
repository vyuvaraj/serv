package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"
)

func TestTimeTravelQueries(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-timetravel-test-*")
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
	bucketName := "timetravel-bucket"
	err = store.CreateBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// Enable Versioning so we can store multiple versions of the same key
	err = store.SetBucketVersioning(ctx, bucketName, "Enabled")
	if err != nil {
		t.Fatalf("failed to enable versioning: %v", err)
	}

	objKey := "history-item"

	// 1. Write Version 1
	content1 := []byte("version one data content")
	ver1, err := store.PutObject(ctx, bucketName, objKey, bytes.NewReader(content1), int64(len(content1)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put version 1: %v", err)
	}

	// Sleep briefly to ensure distinct LastModified timestamps
	time.Sleep(100 * time.Millisecond)
	t1 := time.Now()
	time.Sleep(100 * time.Millisecond)

	// 2. Write Version 2
	content2 := []byte("version two data content")
	ver2, err := store.PutObject(ctx, bucketName, objKey, bytes.NewReader(content2), int64(len(content2)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put version 2: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	t2 := time.Now()
	time.Sleep(100 * time.Millisecond)

	// 3. Write Version 3
	content3 := []byte("version three data content")
	_, err = store.PutObject(ctx, bucketName, objKey, bytes.NewReader(content3), int64(len(content3)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put version 3: %v", err)
	}

	// Query at T1 (should return Version 1)
	ctxT1 := context.WithValue(ctx, TimeTravelContextKey, t1)
	reader1, meta1, err := store.GetObject(ctxT1, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get version at T1: %v", err)
	}
	defer reader1.Close()
	data1, _ := io.ReadAll(reader1)
	if !bytes.Equal(data1, content1) {
		t.Errorf("expected %q, got %q", content1, data1)
	}
	if meta1.VersionID != ver1.VersionID {
		t.Errorf("expected version ID %s, got %s", ver1.VersionID, meta1.VersionID)
	}

	// Query at T2 (should return Version 2)
	ctxT2 := context.WithValue(ctx, TimeTravelContextKey, t2)
	reader2, meta2, err := store.GetObject(ctxT2, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get version at T2: %v", err)
	}
	defer reader2.Close()
	data2, _ := io.ReadAll(reader2)
	if !bytes.Equal(data2, content2) {
		t.Errorf("expected %q, got %q", content2, data2)
	}
	if meta2.VersionID != ver2.VersionID {
		t.Errorf("expected version ID %s, got %s", ver2.VersionID, meta2.VersionID)
	}

	// Query without time travel (should return latest - Version 3)
	readerLatest, _, err := store.GetObject(ctx, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get latest version: %v", err)
	}
	defer readerLatest.Close()
	dataLatest, _ := io.ReadAll(readerLatest)
	if !bytes.Equal(dataLatest, content3) {
		t.Errorf("expected %q, got %q", content3, dataLatest)
	}
}
