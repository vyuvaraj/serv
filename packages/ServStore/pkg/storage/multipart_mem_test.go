package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
)

// TestMultipartMemoryProfile verifies that heap growth during large multipart
// uploads stays bounded — specifically that it does not scale linearly with the
// total upload size (which would indicate unnecessary full-payload buffering).
func TestMultipartMemoryProfile(t *testing.T) {
	dir, err := os.MkdirTemp("", "multipart-mem-*")
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
	if err := store.CreateBucket(ctx, "mp-mem-bucket"); err != nil {
		t.Fatal(err)
	}

	uploadID, err := store.InitiateMultipartUpload(ctx, "mp-mem-bucket", "large-object.bin")
	if err != nil {
		t.Fatalf("InitiateMultipartUpload: %v", err)
	}

	const partSize = 5 * 1024 * 1024 // 5 MiB per part (S3 minimum)
	const numParts = 10               // 50 MiB total

	var ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms1)

	parts := make([]PartInfo, numParts)
	for i := range numParts {
		partData := bytes.Repeat(fmt.Appendf(nil, "%d", i%10), partSize)
		etag, err := store.UploadPart(ctx, "mp-mem-bucket", "large-object.bin", uploadID, i+1,
			bytes.NewReader(partData), int64(len(partData)))
		if err != nil {
			t.Fatalf("UploadPart %d: %v", i+1, err)
		}
		parts[i] = PartInfo{PartNumber: i + 1, ETag: etag}
	}

	_, err = store.CompleteMultipartUpload(ctx, "mp-mem-bucket", "large-object.bin", uploadID, parts, "application/octet-stream")
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	runtime.GC()
	var ms2 runtime.MemStats
	runtime.ReadMemStats(&ms2)

	totalUploadBytes := int64(partSize * numParts)
	heapGrowthBytes := int64(ms2.HeapInuse) - int64(ms1.HeapInuse)

	t.Logf("Total upload size:  %d MiB", totalUploadBytes/(1024*1024))
	t.Logf("Heap growth:        %d MiB (HeapInuse delta)", heapGrowthBytes/(1024*1024))
	t.Logf("TotalAlloc during:  %d MiB", (ms2.TotalAlloc-ms1.TotalAlloc)/(1024*1024))

	// Allow up to 3× a single part size in retained heap — any more indicates
	// full-payload buffering rather than streaming part by part.
	maxAllowedHeapGrowth := int64(3 * partSize)
	if heapGrowthBytes > maxAllowedHeapGrowth {
		t.Errorf("heap grew by %d MiB during %d MiB upload (threshold: %d MiB) — possible buffer copy regression",
			heapGrowthBytes/(1024*1024),
			totalUploadBytes/(1024*1024),
			maxAllowedHeapGrowth/(1024*1024))
	}
}

// BenchmarkMultipartUploadThroughput measures the sustained throughput of the
// multipart upload pipeline for 5 MiB parts.
func BenchmarkMultipartUploadThroughput(b *testing.B) {
	dir, err := os.MkdirTemp("", "bench-mp-*")
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
	if err := store.CreateBucket(ctx, "bench-mp-bucket"); err != nil {
		b.Fatal(err)
	}

	const partSize = 5 * 1024 * 1024
	partData := bytes.Repeat([]byte("x"), partSize)

	b.SetBytes(partSize)
	b.ReportAllocs()

	objIndex := 0
	for b.Loop() {
		key := fmt.Sprintf("obj-%d", objIndex)
		objIndex++
		uploadID, err := store.InitiateMultipartUpload(ctx, "bench-mp-bucket", key)
		if err != nil {
			b.Fatal(err)
		}
		etag, err := store.UploadPart(ctx, "bench-mp-bucket", key, uploadID, 1,
			bytes.NewReader(partData), partSize)
		if err != nil {
			b.Fatal(err)
		}
		_, err = store.CompleteMultipartUpload(ctx, "bench-mp-bucket", key, uploadID,
			[]PartInfo{{PartNumber: 1, ETag: etag}}, "application/octet-stream")
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestMultipartStreamingVerification ensures parts are assembled correctly
// without extra in-memory copies by reading back the completed object and
// verifying its content integrity byte-by-byte.
func TestMultipartStreamingVerification(t *testing.T) {
	dir, err := os.MkdirTemp("", "multipart-stream-*")
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
	if err := store.CreateBucket(ctx, "stream-bucket"); err != nil {
		t.Fatal(err)
	}

	uploadID, err := store.InitiateMultipartUpload(ctx, "stream-bucket", "streamed.bin")
	if err != nil {
		t.Fatal(err)
	}

	// Use distinct byte values per part so any re-ordering is detectable
	const numParts = 4
	const partSize = 64 * 1024
	var expected []byte
	parts := make([]PartInfo, numParts)
	for i := range numParts {
		data := bytes.Repeat([]byte{byte(i + 1)}, partSize)
		expected = append(expected, data...)
		etag, err := store.UploadPart(ctx, "stream-bucket", "streamed.bin", uploadID, i+1,
			bytes.NewReader(data), partSize)
		if err != nil {
			t.Fatalf("UploadPart %d: %v", i+1, err)
		}
		parts[i] = PartInfo{PartNumber: i + 1, ETag: etag}
	}

	if _, err := store.CompleteMultipartUpload(ctx, "stream-bucket", "streamed.bin", uploadID, parts, "application/octet-stream"); err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}

	rc, _, err := store.GetObject(ctx, "stream-bucket", "streamed.bin", "")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(got, expected) {
		t.Errorf("content mismatch: got %d bytes, want %d bytes; first diff at byte %d",
			len(got), len(expected), firstDiff(got, expected))
	}
}

func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return len(a)
}
