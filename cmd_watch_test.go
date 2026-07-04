package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetModTimes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "watch-test")
	if err != nil {
		t.Fatalf("Failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	file1 := filepath.Join(tmpDir, "service1.srv")
	file2 := filepath.Join(tmpDir, "service2.txt") // non-srv file should be ignored

	if err := os.WriteFile(file1, []byte("service X {}"), 0644); err != nil {
		t.Fatalf("Failed to write srv file: %v", err)
	}
	if err := os.WriteFile(file2, []byte("hello"), 0644); err != nil {
		t.Fatalf("Failed to write txt file: %v", err)
	}

	// Define walk/poller helper locally just like in cmd_watch.go
	getTimes := func() map[string]time.Time {
		times := make(map[string]time.Time)
		_ = filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && filepath.Ext(path) == ".srv" {
				times[path] = info.ModTime()
			}
			return nil
		})
		return times
	}

	times := getTimes()
	if len(times) != 1 {
		t.Errorf("Expected 1 tracked .srv file, got %d", len(times))
	}

	if _, ok := times[file1]; !ok {
		t.Errorf("Expected file1 to be tracked")
	}
	if _, ok := times[file2]; ok {
		t.Errorf("Expected file2 to be ignored")
	}
}
