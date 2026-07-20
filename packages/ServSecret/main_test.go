package main

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServSecret/pkg/storage"
)

func TestScheduledBackupAndRotation(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "secrets.enc")

	// 1. Setup store
	masterKey := make([]byte, 32)
	rand.Read(masterKey)

	store, err := storage.NewEncryptedFileStore(tmpFile, masterKey)
	if err != nil {
		t.Fatalf("failed to create EncryptedFileStore: %v", err)
	}

	// 2. Set secret (should create database file on disk)
	err = store.Set("default", "bk-key", "bk-value")
	if err != nil {
		t.Fatalf("failed to set secret: %v", err)
	}

	// 3. Test PerformBackup
	err = PerformBackup(tmpFile)
	if err != nil {
		t.Fatalf("PerformBackup failed: %v", err)
	}
	defer os.RemoveAll("backups")

	// Check if file was copied to backup directory
	files, err := os.ReadDir("backups")
	if err != nil || len(files) == 0 {
		t.Fatalf("backup file not created: %v", err)
	}

	// 4. Test PerformKeyRotation
	err = PerformKeyRotation(store)
	if err != nil {
		t.Fatalf("PerformKeyRotation failed: %v", err)
	}

	// Verify we can still read the secret under the rotated key
	val, err := store.Get("default", "bk-key")
	if err != nil || val != "bk-value" {
		t.Errorf("failed to read secret after key rotation: val=%s, err=%v", val, err)
	}
}
