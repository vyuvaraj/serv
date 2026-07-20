package cluster

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"servstore/pkg/storage"

	"github.com/hashicorp/raft"
)

func TestMetadataFSMApply(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servstore-raft-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewLocalStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create local store: %v", err)
	}
	defer store.Close()

	fsm := NewMetadataFSM(store)

	// Test 1: Create Bucket via FSM Apply
	cmd := MetadataCommand{
		Op:         "CreateBucket",
		BucketName: "test-raft-bucket",
	}
	cmdBytes, _ := json.Marshal(cmd)

	logEntry := &raft.Log{
		Data: cmdBytes,
	}

	res := fsm.Apply(logEntry)
	if err, ok := res.(error); ok && err != nil {
		t.Fatalf("FSM Apply failed: %v", err)
	}

	// Verify bucket directory exists
	bucketPath := filepath.Join(tempDir, "test-raft-bucket")
	if _, err := os.Stat(bucketPath); os.IsNotExist(err) {
		t.Error("Expected bucket directory to be created by Raft FSM Apply")
	}

	// Test 2: Set Lifecycle via FSM Apply
	rules := []storage.LifecycleRule{
		{
			ID:             "rule-1",
			Enabled:        true,
			Prefix:         "logs/",
			ExpirationDays: 7,
		},
	}
	rulesBytes, _ := json.Marshal(rules)

	cmdLifecycle := MetadataCommand{
		Op:         "SetLifecycle",
		BucketName: "test-raft-bucket",
		Value:      rulesBytes,
	}
	cmdLifecycleBytes, _ := json.Marshal(cmdLifecycle)

	logEntryLifecycle := &raft.Log{
		Data: cmdLifecycleBytes,
	}

	res = fsm.Apply(logEntryLifecycle)
	if err, ok := res.(error); ok && err != nil {
		t.Fatalf("FSM Apply lifecycle failed: %v", err)
	}

	// Verify lifecycle rules are set
	bucket, err := store.GetBucket(context.Background(), "test-raft-bucket")
	if err != nil {
		t.Fatalf("Failed to retrieve bucket metadata: %v", err)
	}

	if len(bucket.Lifecycle) != 1 || bucket.Lifecycle[0].ID != "rule-1" {
		t.Errorf("Expected lifecycle rule 'rule-1' to be registered, got %+v", bucket.Lifecycle)
	}
}
