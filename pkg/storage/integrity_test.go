package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDataIntegrityValidation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-integrity-test-*")
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
	bucketName := "integrity-bucket"
	err = store.CreateBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	objKey := "corruptible-object"
	content := []byte("highly sensitive data that must remain intact")
	
	// Put object
	ver, err := store.PutObject(ctx, bucketName, objKey, bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put object: %v", err)
	}
	if ver.Checksum == "" {
		t.Fatalf("expected non-empty BLAKE3 checksum in metadata")
	}

	// Read check (should be valid)
	reader, _, err := store.GetObject(ctx, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get intact object: %v", err)
	}
	readContent, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatalf("failed to read intact object: %v", err)
	}
	if !bytes.Equal(readContent, content) {
		t.Fatalf("expected content %q, got %q", content, readContent)
	}

	// Simulate corruption (bit rot) by writing directly to the underlying data path
	dataPath := store.getObjectDataPath(bucketName, objKey, ver.VersionID)
	corruptedContent := []byte("highly sensitive data that must remain INTACT") // changed 'intact' to 'INTACT'
	err = os.WriteFile(dataPath, corruptedContent, 0644)
	if err != nil {
		t.Fatalf("failed to manually corrupt the file: %v", err)
	}

	// Try reading again - should fail on EOF or during read because of checksum mismatch
	reader, _, err = store.GetObject(ctx, bucketName, objKey, "")
	if err != nil {
		// If error is returned immediately during GetObject (e.g. if encrypted at rest, decrypt/integrity fails early)
		if !strings.Contains(err.Error(), "integrity corruption detected") {
			t.Fatalf("expected integrity corruption error, got: %v", err)
		}
		return
	}
	defer reader.Close()

	_, readErr := io.ReadAll(reader)
	if readErr == nil {
		t.Fatalf("expected read to fail with integrity corruption error, but read succeeded")
	}
	if !strings.Contains(readErr.Error(), "integrity corruption detected") {
		t.Fatalf("expected integrity corruption error, got: %v", readErr)
	}
}
