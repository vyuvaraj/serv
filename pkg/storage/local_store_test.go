package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
)

func TestLocalStore(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewLocalStore(tempDir)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}

	ctx := context.Background()

	// 1. Test Create Bucket
	bucketName := "test-bucket"
	err = store.CreateBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	// 2. Test Bucket Exists Error
	err = store.CreateBucket(ctx, bucketName)
	if err != ErrBucketExists {
		t.Fatalf("expected ErrBucketExists, got %v", err)
	}

	// 3. Test List Buckets
	buckets, err := store.ListBuckets(ctx)
	if err != nil {
		t.Fatalf("failed to list buckets: %v", err)
	}
	if len(buckets) != 1 || buckets[0].Name != bucketName {
		t.Fatalf("list buckets returned unexpected result: %v", buckets)
	}

	// 4. Test Versioning Status Default
	bucketMeta, err := store.GetBucket(ctx, bucketName)
	if err != nil {
		t.Fatalf("failed to get bucket: %v", err)
	}
	if bucketMeta.Versioning != "Disabled" {
		t.Fatalf("expected versioning to be Disabled, got %s", bucketMeta.Versioning)
	}

	// 5. Test Put Object (Versioning Disabled)
	objKey := "test-key"
	content1 := []byte("hello world version 1")
	ver1, err := store.PutObject(ctx, bucketName, objKey, bytes.NewReader(content1), int64(len(content1)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put object: %v", err)
	}
	if ver1.VersionID != "null" {
		t.Fatalf("expected version ID to be 'null' when versioning is disabled, got %s", ver1.VersionID)
	}

	// 6. Test Get Object
	reader, getVer1, err := store.GetObject(ctx, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get object: %v", err)
	}
	defer reader.Close()

	readContent, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read content: %v", err)
	}
	if !bytes.Equal(readContent, content1) {
		t.Fatalf("expected content %s, got %s", content1, readContent)
	}
	if getVer1.VersionID != "null" {
		t.Fatalf("expected get version ID to be 'null', got %s", getVer1.VersionID)
	}

	// 7. Test Enable Versioning
	err = store.SetBucketVersioning(ctx, bucketName, "Enabled")
	if err != nil {
		t.Fatalf("failed to enable versioning: %v", err)
	}

	bucketMeta, _ = store.GetBucket(ctx, bucketName)
	if bucketMeta.Versioning != "Enabled" {
		t.Fatalf("expected versioning to be Enabled, got %s", bucketMeta.Versioning)
	}

	// 8. Test Put Object (Versioning Enabled)
	content2 := []byte("hello world version 2")
	ver2, err := store.PutObject(ctx, bucketName, objKey, bytes.NewReader(content2), int64(len(content2)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put object v2: %v", err)
	}
	if ver2.VersionID == "null" || ver2.VersionID == "" {
		t.Fatalf("expected a generated version ID, got %s", ver2.VersionID)
	}

	// Get latest object (should be version 2)
	reader, getVer2, err := store.GetObject(ctx, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get latest object: %v", err)
	}
	readContent2, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(readContent2, content2) {
		t.Fatalf("expected content %s, got %s", content2, readContent2)
	}
	if getVer2.VersionID != ver2.VersionID {
		t.Fatalf("expected version ID %s, got %s", ver2.VersionID, getVer2.VersionID)
	}

	// Get previous version (should be version 1 with ID "null")
	reader, getVer1Prev, err := store.GetObject(ctx, bucketName, objKey, "null")
	if err != nil {
		t.Fatalf("failed to get previous version: %v", err)
	}
	readContent1Prev, _ := io.ReadAll(reader)
	reader.Close()
	if !bytes.Equal(readContent1Prev, content1) {
		t.Fatalf("expected content %s, got %s", content1, readContent1Prev)
	}
	if getVer1Prev.VersionID != "null" {
		t.Fatalf("expected version ID 'null', got %s", getVer1Prev.VersionID)
	}

	// 9. Test List Objects
	objects, _, err := store.ListObjects(ctx, bucketName, "", "", "", 10)
	if err != nil {
		t.Fatalf("failed to list objects: %v", err)
	}
	if len(objects) != 1 || objects[0].Key != objKey {
		t.Fatalf("expected 1 object, got %d", len(objects))
	}

	// 10. Test List Object Versions
	versions, _, err := store.ListObjectVersions(ctx, bucketName, "", "", "", "", 10)
	if err != nil {
		t.Fatalf("failed to list object versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}

	// 11. Test Delete Object (Creates a Delete Marker since versioning is Enabled)
	delVer, err := store.DeleteObject(ctx, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to delete object: %v", err)
	}
	if !delVer.IsDeleteMarker {
		t.Fatalf("expected delete marker to be created")
	}

	// HeadObject on deleted object should fail (404 equivalent)
	_, err = store.HeadObject(ctx, bucketName, objKey, "")
	if err != ErrObjectNotFound {
		t.Fatalf("expected ErrObjectNotFound, got %v", err)
	}

	// 12. List Object Versions should show 3 versions now (including Delete Marker)
	versions, _, err = store.ListObjectVersions(ctx, bucketName, "", "", "", "", 10)
	if err != nil {
		t.Fatalf("failed to list versions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions (2 content + 1 delete marker), got %d", len(versions))
	}

	// 13. Permanently delete the delete marker using its version ID
	_, err = store.DeleteObject(ctx, bucketName, objKey, delVer.VersionID)
	if err != nil {
		t.Fatalf("failed to permanently delete version: %v", err)
	}

	// HeadObject/GetObject should now find version 2 again because version 2 becomes the latest
	reader, getLatest, err := store.GetObject(ctx, bucketName, objKey, "")
	if err != nil {
		t.Fatalf("failed to get object after delete marker removal: %v", err)
	}
	reader.Close()
	if getLatest.VersionID != ver2.VersionID {
		t.Fatalf("expected latest version to be %s, got %s", ver2.VersionID, getLatest.VersionID)
	}
}
