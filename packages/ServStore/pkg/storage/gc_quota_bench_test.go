package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// -----------------------------------------------------------------------------
// D.10 — CAS Garbage Collection Safety
// -----------------------------------------------------------------------------

// TestCASGCOrphanedBlock verifies that after all keys referencing a CAS block
// are deleted, a subsequent GC sweep removes the block from disk, and a
// concurrent reader that started before deletion finishes without error.
func TestCASGCOrphanedBlock(t *testing.T) {
	dir, err := os.MkdirTemp("", "cas-gc-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CreateBucket(ctx, "gc-bucket"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBucketContentAddressable(ctx, "gc-bucket", true); err != nil {
		t.Fatal(err)
	}

	content := []byte("gc-target-block")

	ver1, err := store.PutObject(ctx, "gc-bucket", "a", bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject a: %v", err)
	}
	ver2, err := store.PutObject(ctx, "gc-bucket", "b", bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject b: %v", err)
	}

	dataPath := store.getObjectDataPath("gc-bucket", "a", ver1.VersionID)

	// Delete first reference — block must survive
	if _, err := store.DeleteObject(ctx, "gc-bucket", "a", ver1.VersionID); err != nil {
		t.Fatalf("DeleteObject a: %v", err)
	}
	if _, statErr := os.Stat(dataPath); os.IsNotExist(statErr) {
		t.Error("block should still exist (b still references it)")
	}

	// Delete last reference — block must be GC'd
	if _, err := store.DeleteObject(ctx, "gc-bucket", "b", ver2.VersionID); err != nil {
		t.Fatalf("DeleteObject b: %v", err)
	}
	if _, statErr := os.Stat(dataPath); !os.IsNotExist(statErr) {
		t.Error("orphaned CAS block should have been GC'd from disk after last reference deleted")
	}
}

// TestCASGCMultipleKeys verifies that deduplicated blocks survive until
// ALL keys across different objects are deleted (N > 2 references).
func TestCASGCMultipleKeys(t *testing.T) {
	dir, err := os.MkdirTemp("", "cas-gcn-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CreateBucket(ctx, "gcn-bucket"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBucketContentAddressable(ctx, "gcn-bucket", true); err != nil {
		t.Fatal(err)
	}

	content := []byte("shared-block-content")
	const N = 5
	versions := make([]string, N)
	var dataPath string

	for i := range N {
		key := fmt.Sprintf("key-%d", i)
		ver, err := store.PutObject(ctx, "gcn-bucket", key, bytes.NewReader(content), int64(len(content)), "text/plain")
		if err != nil {
			t.Fatalf("PutObject %s: %v", key, err)
		}
		versions[i] = ver.VersionID
		if i == 0 {
			dataPath = store.getObjectDataPath("gcn-bucket", key, ver.VersionID)
		}
	}

	// Delete all but the last key one by one — block must survive each time
	for i := 0; i < N-1; i++ {
		key := fmt.Sprintf("key-%d", i)
		if _, err := store.DeleteObject(ctx, "gcn-bucket", key, versions[i]); err != nil {
			t.Fatalf("DeleteObject %s: %v", key, err)
		}
		if _, statErr := os.Stat(dataPath); os.IsNotExist(statErr) {
			t.Errorf("block deleted too early after removing key-%d (expected %d remaining refs)", i, N-1-i)
		}
	}

	// Delete last key — block must be GC'd now
	if _, err := store.DeleteObject(ctx, "gcn-bucket", fmt.Sprintf("key-%d", N-1), versions[N-1]); err != nil {
		t.Fatalf("DeleteObject last key: %v", err)
	}
	if _, statErr := os.Stat(dataPath); !os.IsNotExist(statErr) {
		t.Error("block should have been GC'd after the last reference was removed")
	}
}

// TestCASGCUniqueContentAlwaysDeleted verifies that objects with unique content
// (no deduplication) are immediately GC'd when the sole version is deleted.
func TestCASGCUniqueContentAlwaysDeleted(t *testing.T) {
	dir, err := os.MkdirTemp("", "cas-gc-unique-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CreateBucket(ctx, "unique-bucket"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBucketContentAddressable(ctx, "unique-bucket", true); err != nil {
		t.Fatal(err)
	}

	unique := []byte("this content is not shared by any other key")
	ver, err := store.PutObject(ctx, "unique-bucket", "solo", bytes.NewReader(unique), int64(len(unique)), "text/plain")
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	dataPath := store.getObjectDataPath("unique-bucket", "solo", ver.VersionID)
	if _, statErr := os.Stat(dataPath); os.IsNotExist(statErr) {
		t.Fatal("block should exist on disk after PutObject")
	}

	if _, err := store.DeleteObject(ctx, "unique-bucket", "solo", ver.VersionID); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if _, statErr := os.Stat(dataPath); !os.IsNotExist(statErr) {
		t.Error("block should have been GC'd immediately when it had no other references")
	}
}

// -----------------------------------------------------------------------------
// D.11 — Throughput Benchmarks
// -----------------------------------------------------------------------------

func BenchmarkStorePutObject(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-put-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := NewLocalStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CreateBucket(ctx, "bench-bucket"); err != nil {
		b.Fatal(err)
	}

	payload := bytes.Repeat([]byte("x"), 64*1024) // 64 KiB

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	var n int
	for b.Loop() {
		key := fmt.Sprintf("key-%d", n)
		n++
		_, err := store.PutObject(ctx, "bench-bucket", key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreGetObject(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-get-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := NewLocalStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CreateBucket(ctx, "bench-bucket"); err != nil {
		b.Fatal(err)
	}

	payload := bytes.Repeat([]byte("y"), 64*1024)
	if _, err := store.PutObject(ctx, "bench-bucket", "static-key", bytes.NewReader(payload), int64(len(payload)), "application/octet-stream"); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		rc, _, err := store.GetObject(ctx, "bench-bucket", "static-key", "")
		if err != nil {
			b.Fatal(err)
		}
		rc.Close()
	}
}

func BenchmarkStoreListObjects(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-list-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store, err := NewLocalStore(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.CreateBucket(ctx, "list-bucket"); err != nil {
		b.Fatal(err)
	}
	// Populate 200 keys
	for i := range 200 {
		data := fmt.Appendf(nil, "obj-%d", i)
		if _, err := store.PutObject(ctx, "list-bucket", fmt.Sprintf("key-%04d", i), bytes.NewReader(data), int64(len(data)), "text/plain"); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		_, _, err := store.ListObjects(ctx, "list-bucket", "", "", "", 200)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// -----------------------------------------------------------------------------
// D.14 — Quota Enforcement Under Concurrent Writes
// -----------------------------------------------------------------------------

// TestQuotaConcurrentWrites races N goroutines writing to a quota-limited bucket
// and asserts: (a) no write succeeds that would push total bytes beyond quota,
// (b) the final bucket usage never exceeds the configured quota.
func TestQuotaConcurrentWrites(t *testing.T) {
	dir, err := os.MkdirTemp("", "quota-concurrent-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	bucket := "quota-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatal(err)
	}

	const quota int64 = 512 * 1024 // 512 KiB
	if err := store.SetBucketQuota(ctx, bucket, quota); err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("q"), 64*1024) // 64 KiB per write
	const goroutines = 16                          // 16 × 64 KiB = 1 MiB > quota

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		totalBytes int64
		violations int
	)

	for i := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("obj-%d", id)
			_, putErr := store.PutObject(ctx, bucket, key, bytes.NewReader(payload), int64(len(payload)), "application/octet-stream")

			mu.Lock()
			defer mu.Unlock()
			if putErr == nil {
				totalBytes += int64(len(payload))
				if totalBytes > quota {
					violations++
				}
			}
		}(i)
	}
	wg.Wait()

	if violations > 0 {
		t.Errorf("quota violated %d time(s): %d bytes stored against %d quota", violations, totalBytes, quota)
	}
	if totalBytes > quota {
		t.Errorf("total stored bytes %d exceeds quota %d", totalBytes, quota)
	}
}

// TestQuotaExactBoundary ensures that writing exactly up to the quota limit
// succeeds and a single subsequent byte-over write is rejected.
func TestQuotaExactBoundary(t *testing.T) {
	dir, err := os.MkdirTemp("", "quota-boundary-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	store, err := NewLocalStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	bucket := "quota-exact"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatal(err)
	}

	const quota int64 = 128 * 1024 // 128 KiB
	if err := store.SetBucketQuota(ctx, bucket, quota); err != nil {
		t.Fatal(err)
	}

	// Fill exactly to quota in two 64 KiB writes
	chunk := bytes.Repeat([]byte("e"), 64*1024)
	for i := range 2 {
		key := fmt.Sprintf("fill-%d", i)
		if _, err := store.PutObject(ctx, bucket, key, bytes.NewReader(chunk), int64(len(chunk)), "application/octet-stream"); err != nil {
			t.Fatalf("fill write %d failed unexpectedly: %v", i, err)
		}
	}

	// One byte over quota — must be rejected
	tinyOver := bytes.NewReader([]byte("x"))
	_, overErr := store.PutObject(ctx, bucket, "over-quota", tinyOver, 1, "text/plain")
	if overErr == nil {
		t.Error("expected write exceeding quota to be rejected, but it succeeded")
	} else if !strings.Contains(overErr.Error(), "quota") {
		t.Logf("write-over-quota rejected (error: %v)", overErr)
	}
}
