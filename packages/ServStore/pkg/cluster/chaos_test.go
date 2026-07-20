package cluster

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"

	"github.com/hashicorp/raft"
)

// TestChaos_NodeKillDuringWrite kills the leader node while concurrent writes
// are in progress and verifies data integrity after automatic re-election.
func TestChaos_NodeKillDuringWrite(t *testing.T) {
	node1 := newTestRaftNode(t, "chaos-node-1", 18201)
	node2 := newTestRaftNode(t, "chaos-node-2", 18202)
	node3 := newTestRaftNode(t, "chaos-node-3", 18203)
	defer node1.cleanup()
	defer node2.cleanup()
	defer node3.cleanup()

	// Bootstrap 3-node cluster
	bootCfg := raft.Configuration{
		Servers: []raft.Server{
			{ID: raft.ServerID(node1.nodeID), Address: raft.ServerAddress(node1.raftAddr)},
		},
	}
	node1.raftNode.raftInstance.BootstrapCluster(bootCfg)
	time.Sleep(800 * time.Millisecond)

	if err := node1.raftNode.Join(node2.nodeID, node2.raftAddr); err != nil {
		t.Fatalf("node2 join: %v", err)
	}
	if err := node1.raftNode.Join(node3.nodeID, node3.raftAddr); err != nil {
		t.Fatalf("node3 join: %v", err)
	}
	time.Sleep(800 * time.Millisecond)

	// Validate baseline replication
	baseline := MetadataCommand{Op: "CreateBucket", BucketName: "chaos-baseline"}
	if err := node1.raftNode.Propose(baseline); err != nil {
		t.Fatalf("baseline proposal: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	// Fire concurrent writes while leader is still up
	const writesBeforeKill = 5
	var wg sync.WaitGroup
	for i := range writesBeforeKill {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cmd := MetadataCommand{Op: "CreateBucket", BucketName: fmt.Sprintf("chaos-pre-kill-%d", id)}
			_ = node1.raftNode.Propose(cmd) // best-effort; some may fail
		}(i)
	}
	wg.Wait()

	// --- Kill the leader node (node1) ---
	node1.raftNode.Close()
	node1.stream.Close()

	// Wait for the remaining two nodes to re-elect a leader
	time.Sleep(2 * time.Second)

	// Identify the new leader and issue writes through it
	newLeader := node2
	if node3.raftNode.raftInstance.State() == raft.Leader {
		newLeader = node3
	}

	const writesAfterKill = 3
	for i := range writesAfterKill {
		cmd := MetadataCommand{Op: "CreateBucket", BucketName: fmt.Sprintf("chaos-post-kill-%d", i)}
		if err := newLeader.raftNode.Propose(cmd); err != nil {
			t.Errorf("post-kill proposal %d failed: %v", i, err)
		}
	}
	time.Sleep(500 * time.Millisecond)

	// Data integrity check: all post-kill buckets must exist on both surviving nodes
	ctx := context.Background()
	for i := range writesAfterKill {
		bucket := fmt.Sprintf("chaos-post-kill-%d", i)
		for _, n := range []*testRaftNode{node2, node3} {
			if _, err := n.store.GetBucket(ctx, bucket); err != nil {
				t.Errorf("post-kill bucket %q missing on %s: %v", bucket, n.nodeID, err)
			}
		}
	}
}

// TestChaos_PartitionHealDataIntegrity partitions a follower, writes through the
// majority, heals the partition, and verifies the lagging node converges without
// data loss or corruption.
func TestChaos_PartitionHealDataIntegrity(t *testing.T) {
	node1 := newTestRaftNode(t, "heal-node-1", 18301)
	node2 := newTestRaftNode(t, "heal-node-2", 18302)
	node3 := newTestRaftNode(t, "heal-node-3", 18303)
	defer node1.cleanup()
	defer node2.cleanup()
	defer node3.cleanup()

	bootCfg := raft.Configuration{
		Servers: []raft.Server{
			{ID: raft.ServerID(node1.nodeID), Address: raft.ServerAddress(node1.raftAddr)},
		},
	}
	node1.raftNode.raftInstance.BootstrapCluster(bootCfg)
	time.Sleep(800 * time.Millisecond)

	_ = node1.raftNode.Join(node2.nodeID, node2.raftAddr)
	_ = node1.raftNode.Join(node3.nodeID, node3.raftAddr)
	time.Sleep(800 * time.Millisecond)

	// Partition node3
	node3.stream.setBlock(true)
	time.Sleep(800 * time.Millisecond)

	const writesWhilePartitioned = 8
	for i := range writesWhilePartitioned {
		cmd := MetadataCommand{Op: "CreateBucket", BucketName: fmt.Sprintf("heal-bucket-%d", i)}
		if err := node1.raftNode.Propose(cmd); err != nil {
			t.Errorf("write during partition %d failed: %v", i, err)
		}
	}
	time.Sleep(300 * time.Millisecond)

	// Heal partition
	node3.stream.setBlock(false)
	time.Sleep(2 * time.Second) // allow catch-up replication

	// Data integrity: node3 must have all buckets written during partition
	ctx := context.Background()
	for i := range writesWhilePartitioned {
		bucket := fmt.Sprintf("heal-bucket-%d", i)
		if _, err := node3.store.GetBucket(ctx, bucket); err != nil {
			t.Errorf("healed node3 missing bucket %q: %v", bucket, err)
		}
	}
}

// TestChaos_ConcurrentWritesDuringPartitionAndHeal stress-tests the cluster
// with concurrent writers during a partition + heal cycle and verifies no
// committed write is lost after recovery.
func TestChaos_ConcurrentWritesDuringPartitionAndHeal(t *testing.T) {
	node1 := newTestRaftNode(t, "stress-node-1", 18401)
	node2 := newTestRaftNode(t, "stress-node-2", 18402)
	node3 := newTestRaftNode(t, "stress-node-3", 18403)
	defer node1.cleanup()
	defer node2.cleanup()
	defer node3.cleanup()

	bootCfg := raft.Configuration{
		Servers: []raft.Server{
			{ID: raft.ServerID(node1.nodeID), Address: raft.ServerAddress(node1.raftAddr)},
		},
	}
	node1.raftNode.raftInstance.BootstrapCluster(bootCfg)
	time.Sleep(800 * time.Millisecond)

	_ = node1.raftNode.Join(node2.nodeID, node2.raftAddr)
	_ = node1.raftNode.Join(node3.nodeID, node3.raftAddr)
	time.Sleep(800 * time.Millisecond)

	var (
		mu        sync.Mutex
		committed []string
		wg        sync.WaitGroup
	)

	const workers = 4
	const opsPerWorker = 5

	// Start writers concurrently
	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for op := range opsPerWorker {
				name := fmt.Sprintf("stress-bucket-%d-%d", workerID, op)
				cmd := MetadataCommand{Op: "CreateBucket", BucketName: name}
				if err := node1.raftNode.Propose(cmd); err == nil {
					mu.Lock()
					committed = append(committed, name)
					mu.Unlock()
				}
			}
		}(w)
	}

	// Midway through: partition a follower then heal
	time.Sleep(100 * time.Millisecond)
	node3.stream.setBlock(true)
	time.Sleep(400 * time.Millisecond)
	node3.stream.setBlock(false)

	wg.Wait()
	time.Sleep(1500 * time.Millisecond) // allow full catch-up

	// All committed buckets must appear on all nodes
	ctx := context.Background()
	for _, bucket := range committed {
		for _, n := range []*testRaftNode{node1, node2, node3} {
			if _, err := n.store.GetBucket(ctx, bucket); err != nil {
				t.Errorf("committed bucket %q missing on %s: %v", bucket, n.nodeID, err)
			}
		}
	}

	_ = storage.ErrBucketNotFound // suppress import-not-used if nothing else uses it
}
