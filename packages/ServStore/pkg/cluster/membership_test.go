package cluster

import (
	"testing"
	"time"
)

func TestGossipMerge(t *testing.T) {
	nodeA := NewMembershipManager("node-a", "localhost:8080", "")
	nodeB := NewMembershipManager("node-b", "localhost:8081", "")

	// Node A initiates a gossip to Node B
	payloadA := GossipPayload{
		SourceNode: *nodeA.localNode,
		Peers:      make(map[string]*NodeInfo),
	}

	replyB := nodeB.MergeGossip(payloadA)

	// Node B should now have Node A in its peers map
	nodesB := nodeB.GetNodes()
	foundA := false
	for _, node := range nodesB {
		if node.NodeID == "node-a" {
			foundA = true
			if node.Address != "localhost:8080" {
				t.Errorf("Expected address localhost:8080, got %s", node.Address)
			}
			break
		}
	}
	if !foundA {
		t.Error("Node B failed to discover Node A during gossip merge")
	}

	// The reply from Node B should contain Node B info
	if replyB.SourceNode.NodeID != "node-b" {
		t.Errorf("Expected response from node-b, got %s", replyB.SourceNode.NodeID)
	}
}

func TestMembershipTimeouts(t *testing.T) {
	mm := NewMembershipManager("node-a", "localhost:8080", "localhost:8081")

	// Manually insert an active peer with a dynamic ID
	mm.mu.Lock()
	peer := &NodeInfo{
		NodeID:   "node-b",
		Address:  "localhost:8081",
		Status:   "online",
		LastSeen: time.Now().Add(-20 * time.Second), // simulated timeout (exceeding 10s)
	}
	mm.peers["node-b"] = peer
	mm.mu.Unlock()

	// Check timeouts
	mm.checkTimeouts()

	// Peer should be marked offline
	mm.mu.RLock()
	p := mm.peers["node-b"]
	mm.mu.RUnlock()

	if p.Status != "offline" {
		t.Errorf("Expected status to be offline, got %s", p.Status)
	}
}
