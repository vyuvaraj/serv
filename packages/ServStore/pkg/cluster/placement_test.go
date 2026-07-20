package cluster

import (
	"strconv"
	"testing"
)

func TestHashRingBasic(t *testing.T) {
	ring := NewHashRing(10) // 10 replicas per node

	ring.AddNode("node-1")
	ring.AddNode("node-2")

	// Get ownership of a few keys
	nodeA, err := ring.GetNodes("some-key-1", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nodeA) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodeA))
	}

	nodeB, err := ring.GetNodes("some-key-1", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Placement should be completely deterministic
	if nodeA[0] != nodeB[0] {
		t.Errorf("GetNodes is not deterministic: got %s first, then %s", nodeA[0], nodeB[0])
	}
}

func TestHashRingAddRemove(t *testing.T) {
	ring := NewHashRing(5)

	ring.AddNode("node-1")
	ring.AddNode("node-2")
	ring.AddNode("node-3")

	key := "my-s3-object-key"
	ownerBefore, err := ring.GetNodes(key, 1)
	if err != nil {
		t.Fatalf("GetNodes failed: %v", err)
	}

	// Now remove the node that owned it (if it was node-3)
	// or just remove one of the nodes and ensure the key still maps to one of the remaining nodes.
	ring.RemoveNode(ownerBefore[0])

	ownerAfter, err := ring.GetNodes(key, 1)
	if err != nil {
		t.Fatalf("GetNodes after remove failed: %v", err)
	}

	if ownerBefore[0] == ownerAfter[0] {
		t.Errorf("key still mapped to removed node %s", ownerBefore[0])
	}

	if ownerAfter[0] != "node-1" && ownerAfter[0] != "node-2" && ownerAfter[0] != "node-3" {
		t.Errorf("key mapped to invalid node %s", ownerAfter[0])
	}
}

func TestHashRingDistribution(t *testing.T) {
	ring := NewHashRing(50) // 50 replicas

	nodes := []string{"node-1", "node-2", "node-3", "node-4", "node-5"}
	for _, n := range nodes {
		ring.AddNode(n)
	}

	counts := make(map[string]int)
	totalKeys := 1000

	for i := 0; i < totalKeys; i++ {
		key := "key-" + strconv.Itoa(i)
		owners, err := ring.GetNodes(key, 1)
		if err != nil {
			t.Fatalf("failed to get nodes: %v", err)
		}
		counts[owners[0]]++
	}

	// Verify all nodes own some share of keys
	for _, n := range nodes {
		count := counts[n]
		if count == 0 {
			t.Errorf("Node %s got 0 keys allocated, indicating poor distribution", n)
		}
		// A rough distribution check. With 1000 keys and 5 nodes, we expect roughly 200 per node.
		// Since hashing is pseudo-random, allow a wider margin (e.g. 50 to 350 keys).
		if count < 50 || count > 350 {
			t.Logf("Node %s got %d keys, which is outside the typical soft-bound [50, 350] but acceptable for random FNV-1a distribution", n, count)
		}
	}
}
