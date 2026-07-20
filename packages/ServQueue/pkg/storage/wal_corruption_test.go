package storage

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestWALCorruptionRecovery appends multiple entries to the WAL,
// corrupts the last entry (partial write/corrupt checksum), and verifies that
// Recover successfully returns all valid entries before the corruption point.
func TestWALCorruptionRecovery(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test_corruption.wal")

	// 1. Open WAL and append 3 valid entries
	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}

	entries := []struct {
		topic   string
		payload string
	}{
		{"topic-1", "payload-data-1"},
		{"topic-2", "payload-data-2"},
		{"topic-3", "payload-data-3"},
	}

	for _, entry := range entries {
		err := wal.Append(entry.topic, entry.payload)
		if err != nil {
			t.Fatalf("failed to append: %v", err)
		}
	}
	wal.Close()

	// 2. Corrupt the WAL file by appending some junk bytes to simulate a partial/corrupted write
	file, err := os.OpenFile(walPath, os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		t.Fatalf("failed to open WAL file for corruption: %v", err)
	}

	// Write invalid headers and truncated payload
	junkHeader := make([]byte, 16)
	binary.BigEndian.PutUint32(junkHeader[0:4], 100) // Topic length 100
	binary.BigEndian.PutUint32(junkHeader[4:8], 200) // Payload length 200
	binary.BigEndian.PutUint64(junkHeader[8:16], 123456789)
	_, _ = file.Write(junkHeader)
	_, _ = file.Write([]byte("short-topic")) // Less than 100 bytes (unexpected EOF simulation)
	file.Close()

	// 3. Re-open WAL and trigger Recovery
	wal2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("failed to re-open WAL: %v", err)
	}
	defer wal2.Close()

	recovered, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed unexpectedly: %v", err)
	}

	// Assert we recovered exactly the 3 valid entries
	if len(recovered) != 3 {
		t.Errorf("expected to recover exactly 3 valid entries, got %d", len(recovered))
	} else {
		for i := 0; i < 3; i++ {
			if recovered[i].Topic != entries[i].topic {
				t.Errorf("entry %d topic mismatch: got %q, want %q", i, recovered[i].Topic, entries[i].topic)
			}
			if recovered[i].Payload != entries[i].payload {
				t.Errorf("entry %d payload mismatch: got %q, want %q", i, recovered[i].Payload, entries[i].payload)
			}
		}
	}
}

// TestWALChecksumMismatchVerification verifies that checksum mismatch is detected
// on a modified entry, and the file gets truncated to clean state.
func TestWALChecksumMismatchVerification(t *testing.T) {
	tempDir := t.TempDir()
	walPath := filepath.Join(tempDir, "test_checksum.wal")

	wal, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("failed to open WAL: %v", err)
	}

	err = wal.Append("secure-topic", "highly-confidential-payload")
	if err != nil {
		t.Fatalf("failed to append: %v", err)
	}
	wal.Close()

	// Modify payload bytes directly in the file
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("failed to read WAL: %v", err)
	}

	// Change "highly" to "low-ly"
	for i := range data {
		if i+6 <= len(data) && string(data[i:i+6]) == "highly" {
			copy(data[i:i+6], []byte("low-ly"))
			break
		}
	}

	err = os.WriteFile(walPath, data, 0666)
	if err != nil {
		t.Fatalf("failed to write corrupted WAL: %v", err)
	}

	wal2, err := OpenWAL(walPath)
	if err != nil {
		t.Fatalf("failed to re-open WAL: %v", err)
	}
	defer wal2.Close()

	recovered, err := wal2.Recover()
	if err != nil {
		t.Fatalf("Recover failed unexpectedly: %v", err)
	}

	if len(recovered) != 0 {
		t.Errorf("expected 0 recovered entries due to checksum mismatch corruption, got %d", len(recovered))
	}

	// Check if file is truncated
	info, err := os.Stat(walPath)
	if err != nil {
		t.Fatalf("failed to stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected WAL file to be truncated to 0 size, got %d", info.Size())
	}
}
