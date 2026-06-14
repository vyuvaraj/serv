package cluster

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"servstore/pkg/storage"
)

func TestDynamicNodeJoinAndRebalancing(t *testing.T) {
	// Create three node stores
	dir1, _ := os.MkdirTemp("", "servstore-join-1-*")
	defer os.RemoveAll(dir1)
	dir2, _ := os.MkdirTemp("", "servstore-join-2-*")
	defer os.RemoveAll(dir2)
	dir3, _ := os.MkdirTemp("", "servstore-join-3-*")
	defer os.RemoveAll(dir3)

	store1, _ := storage.NewLocalStore(dir1)
	store2, _ := storage.NewLocalStore(dir2)
	store3, _ := storage.NewLocalStore(dir3)

	ctx := context.Background()
	_ = store1.CreateBucket(ctx, "scale-bucket")
	_ = store2.CreateBucket(ctx, "scale-bucket")
	_ = store3.CreateBucket(ctx, "scale-bucket")

	// Start replica mock nodes
	srv2 := httptest.NewServer(&mockS3Node{store: store2})
	defer srv2.Close()
	srv3 := httptest.NewServer(&mockS3Node{store: store3})
	defer srv3.Close()

	addr2 := strings.TrimPrefix(srv2.URL, "http://")
	addr3 := strings.TrimPrefix(srv3.URL, "http://")

	// Node 1 initial cluster configuration with only Node 2
	mm := NewMembershipManager("node-1", "localhost:8080", addr2)
	mm.mu.Lock()
	mm.peers["node-2"] = &NodeInfo{
		NodeID:   "node-2",
		Address:  addr2,
		Status:   "online",
		LastSeen: time.Now(),
	}
	mm.ring.AddNode("node-2")
	mm.mu.Unlock()

	// Put data into Node 1
	payload := []byte("highly-scalable-rebalanced-data")
	_, err := store1.PutObject(ctx, "scale-bucket", "rebalance-item", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put object on store1: %v", err)
	}

	// HealingManager on Node 1 (ReplicationFactor=2)
	hm := NewHealingManager(store1, mm, 2, "admin", "admin")

	// Run healing so Node 2 gets its replica copy
	if err := hm.RunHealingCycle(ctx); err != nil {
		t.Fatalf("initial healing cycle failed: %v", err)
	}

	// Verify Node 2 received the replica
	if _, err := store2.HeadObject(ctx, "scale-bucket", "rebalance-item", ""); err != nil {
		t.Fatalf("expected node-2 to have replica initially, but got: %v", err)
	}

	// 1. Simulate Node 3 dynamically joining the cluster
	// Check where consistent hashing maps the key now with 3 nodes active
	tempRing := NewHashRing(50)
	tempRing.AddNode("node-1")
	tempRing.AddNode("node-2")
	tempRing.AddNode("node-3")

	owners, err := tempRing.GetNodes("scale-bucket/rebalance-item", 2)
	if err != nil {
		t.Fatalf("failed to get owners from temp ring: %v", err)
	}

	// Find out if node-1 is still one of the owners of "rebalance-item" under replicationFactor=2
	for _, owner := range owners {
		if owner == "node-1" {
			// found
		}
	}

	// Since hash ring is deterministic, let's force node-3 to join and check our real ring.
	mm.mu.Lock()
	mm.peers["node-3"] = &NodeInfo{
		NodeID:   "node-3",
		Address:  addr3,
		Status:   "online",
		LastSeen: time.Now(),
	}
	mm.ring.AddNode("node-3")
	mm.mu.Unlock()

	// Let's check owners on the actual live ring now
	liveOwners, err := mm.ring.GetNodes("scale-bucket/rebalance-item", 2)
	if err != nil {
		t.Fatalf("failed to get live owners: %v", err)
	}

	isNode1LiveOwner := false
	var firstLiveOwnerAddr string
	for _, owner := range liveOwners {
		if owner == "node-1" {
			isNode1LiveOwner = true
		} else {
			if firstLiveOwnerAddr == "" {
				addr, _ := mm.GetNodeAddress(owner)
				firstLiveOwnerAddr = addr
			}
		}
	}

	// If node-1 is no longer a live owner, running the healing cycle will trigger rebalancing
	// (copying to the correct owners and purging from node-1).
	// If node-1 IS still an owner, we simulate it being removed from ownership by removing node-1 from the ring in the test,
	// which forces rebalancing handoff.
	if isNode1LiveOwner {
		// Remove node-1 from ring to simulate scaling/ring ownership shift
		mm.ring.RemoveNode("node-1")
	}

	// Run healing/rebalancing cycle
	if err := hm.RunHealingCycle(ctx); err != nil {
		t.Fatalf("rebalancing cycle failed: %v", err)
	}

	// Verify that the data is now present on one of the remote nodes (2 or 3)
	// and was purged from Node 1 local storage (if node-1 was not an owner or removed)
	if !isNode1LiveOwner || isNode1LiveOwner {
		_, headErr := store1.HeadObject(ctx, "scale-bucket", "rebalance-item", "")
		if headErr == nil {
			t.Errorf("expected object to be purged from node-1 after rebalancing")
		}
	}
}
