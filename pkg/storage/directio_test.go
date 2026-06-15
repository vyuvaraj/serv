package storage

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestPerformance_DirectIOWrites(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-directio-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "direct.dat")

	// Generate unaligned data payload to check truncation behavior
	size := 123456
	data := make([]byte, size)
	rnd := rand.New(rand.NewSource(101))
	_, _ = rnd.Read(data)

	// Write using Direct I/O
	err = WriteFileDirectIO(filePath, data)
	if err != nil {
		t.Fatalf("DirectIO write failed: %v", err)
	}

	// Read and verify correct content-length and matching bytes
	readData, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}

	if len(readData) != size {
		t.Errorf("size mismatch: expected %d bytes, got %d", size, len(readData))
	}

	if !bytes.Equal(readData, data) {
		t.Error("content corruption detected in DirectIO roundtrip")
	}
}
