package cluster

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"servstore/pkg/storage"

	"github.com/hashicorp/raft"
)

// partitionableStreamLayer wraps a net.Listener and implements raft.StreamLayer.
// It allows simulating network partitions by blocking incoming/outgoing connections.
type partitionableStreamLayer struct {
	net.Listener
	mu        sync.Mutex
	isBlocked bool
}

func newPartitionableStreamLayer(l net.Listener) *partitionableStreamLayer {
	return &partitionableStreamLayer{
		Listener: l,
	}
}

func (p *partitionableStreamLayer) setBlock(blocked bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.isBlocked = blocked
}

func (p *partitionableStreamLayer) Accept() (net.Conn, error) {
	for {
		c, err := p.Listener.Accept()
		if err != nil {
			return nil, err
		}

		p.mu.Lock()
		if p.isBlocked {
			c.Close()
			p.mu.Unlock()
			continue
		}
		p.mu.Unlock()

		return &partitionableConn{Conn: c, parent: p}, nil
	}
}

func (p *partitionableStreamLayer) Dial(address raft.ServerAddress, timeout time.Duration) (net.Conn, error) {
	p.mu.Lock()
	if p.isBlocked {
		p.mu.Unlock()
		return nil, fmt.Errorf("network link partitioned")
	}
	p.mu.Unlock()

	dialer := &net.Dialer{Timeout: timeout}
	c, err := dialer.Dial("tcp", string(address))
	if err != nil {
		return nil, err
	}

	return &partitionableConn{Conn: c, parent: p}, nil
}

// partitionableConn wraps net.Conn to intercept read/write calls if partitioned
type partitionableConn struct {
	net.Conn
	parent *partitionableStreamLayer
}

func (c *partitionableConn) Read(b []byte) (n int, err error) {
	c.parent.mu.Lock()
	blocked := c.parent.isBlocked
	c.parent.mu.Unlock()
	if blocked {
		return 0, fmt.Errorf("connection partitioned")
	}
	return c.Conn.Read(b)
}

func (c *partitionableConn) Write(b []byte) (n int, err error) {
	c.parent.mu.Lock()
	blocked := c.parent.isBlocked
	c.parent.mu.Unlock()
	if blocked {
		return 0, fmt.Errorf("connection partitioned")
	}
	return c.Conn.Write(b)
}

type testRaftNode struct {
	nodeID     string
	raftNode   *RaftNode
	store      storage.StorageEngine
	dataDir    string
	stream     *partitionableStreamLayer
	raftPort   int
	raftAddr   string
}

func newTestRaftNode(t *testing.T, id string, port int) *testRaftNode {
	t.Helper()
	tempDir, err := os.MkdirTemp("", fmt.Sprintf("servstore-resilience-%s-*", id))
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	store, err := storage.NewLocalStore(tempDir)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to create store: %v", err)
	}

	raftBindAddr := fmt.Sprintf("127.0.0.1:%d", port)
	fsm := NewMetadataFSM(store)

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)
	config.HeartbeatTimeout = 40 * time.Millisecond
	config.ElectionTimeout = 40 * time.Millisecond
	config.CommitTimeout = 10 * time.Millisecond
	config.LeaderLeaseTimeout = 20 * time.Millisecond

	addr, err := net.ResolveTCPAddr("tcp", raftBindAddr)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("resolve TCP addr failed: %v", err)
	}

	rawListener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("listen failed: %v", err)
	}

	stream := newPartitionableStreamLayer(rawListener)
	transport := raft.NewNetworkTransport(stream, 3, 2*time.Second, os.Stderr)

	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()
	snapStore := raft.NewDiscardSnapshotStore()

	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapStore, transport)
	if err != nil {
		rawListener.Close()
		os.RemoveAll(tempDir)
		t.Fatalf("raft creation failed: %v", err)
	}

	node := &RaftNode{
		raftInstance: r,
		fsm:          fsm,
		transport:    transport,
	}

	return &testRaftNode{
		nodeID:   id,
		raftNode: node,
		store:    store,
		dataDir:  tempDir,
		stream:   stream,
		raftPort: port,
		raftAddr: raftBindAddr,
	}
}

func (n *testRaftNode) cleanup() {
	n.raftNode.Close()
	n.stream.Close()
	if ls, ok := n.store.(*storage.LocalStore); ok {
		ls.Close()
	}
	os.RemoveAll(n.dataDir)
}

func TestResilience_MetadataConsistencyUnderPartition(t *testing.T) {
	// Create a 3-node cluster
	node1 := newTestRaftNode(t, "node-1", 18091)
	node2 := newTestRaftNode(t, "node-2", 18092)
	node3 := newTestRaftNode(t, "node-3", 18093)
	defer node1.cleanup()
	defer node2.cleanup()
	defer node3.cleanup()

	// Bootstrap Node 1
	config := raft.Configuration{
		Servers: []raft.Server{
			{
				ID:      raft.ServerID(node1.nodeID),
				Address: raft.ServerAddress(node1.raftAddr),
			},
		},
	}
	node1.raftNode.raftInstance.BootstrapCluster(config)

	// Wait for node-1 to become leader
	time.Sleep(300 * time.Millisecond)

	// Join node-2 and node-3 to the cluster
	err := node1.raftNode.Join(node2.nodeID, node2.raftAddr)
	if err != nil {
		t.Fatalf("node-2 join failed: %v", err)
	}
	err = node1.raftNode.Join(node3.nodeID, node3.raftAddr)
	if err != nil {
		t.Fatalf("node-3 join failed: %v", err)
	}

	// Wait for consensus replication configuration to settle
	time.Sleep(300 * time.Millisecond)

	// Propose initial metadata change to verify replication works
	cmd1 := MetadataCommand{
		Op:         "CreateBucket",
		BucketName: "resilience-bucket-1",
	}
	if err := node1.raftNode.Propose(cmd1); err != nil {
		t.Fatalf("Initial proposal failed: %v", err)
	}

	// Verify replication across all nodes
	time.Sleep(100 * time.Millisecond)
	for _, n := range []*testRaftNode{node1, node2, node3} {
		if _, err := n.store.GetBucket(context.Background(), "resilience-bucket-1"); err != nil {
			t.Errorf("Bucket replication failed on %s: %v", n.nodeID, err)
		}
	}

	// --- STEP 1: Simulate network partition (partition node-3 away from nodes 1 and 2) ---
	node3.stream.setBlock(true)

	// Wait for Raft node timeouts to detect partition
	time.Sleep(300 * time.Millisecond)

	// Proposal on Node 1 (retains majority: node-1 and node-2) should STILL succeed
	cmd2 := MetadataCommand{
		Op:         "CreateBucket",
		BucketName: "resilience-bucket-majority",
	}
	if err := node1.raftNode.Propose(cmd2); err != nil {
		t.Errorf("Majority partition failed to accept proposal: %v", err)
	}

	// Proposal on the isolated Node 3 (minority partition) must fail
	cmd3 := MetadataCommand{
		Op:         "CreateBucket",
		BucketName: "resilience-bucket-minority",
	}
	if err := node3.raftNode.Propose(cmd3); err == nil {
		t.Error("Expected isolated minority node proposal to fail, but it succeeded")
	}

	// --- STEP 2: Heal partition ---
	node3.stream.setBlock(false)

	// Wait for Raft heartbeats to catch up and sync state
	time.Sleep(300 * time.Millisecond)

	// Verify Node 3 caught up and has 'resilience-bucket-majority'
	if _, err := node3.store.GetBucket(context.Background(), "resilience-bucket-majority"); err != nil {
		t.Errorf("Healed node-3 failed to sync metadata state from majority leader: %v", err)
	}
}

func TestResilience_MetadataLinearizability(t *testing.T) {
	node1 := newTestRaftNode(t, "lin-node-1", 19091)
	defer node1.cleanup()

	config := raft.Configuration{
		Servers: []raft.Server{
			{
				ID:      raft.ServerID(node1.nodeID),
				Address: raft.ServerAddress(node1.raftAddr),
			},
		},
	}
	node1.raftNode.raftInstance.BootstrapCluster(config)
	time.Sleep(300 * time.Millisecond)

	var wg sync.WaitGroup
	workers := 5
	opsPerWorker := 10

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < opsPerWorker; j++ {
				bucketName := fmt.Sprintf("lin-bucket-%d-%d", workerID, j)
				cmd := MetadataCommand{
					Op:         "CreateBucket",
					BucketName: bucketName,
				}
				_ = node1.raftNode.Propose(cmd)
			}
		}(i)
	}

	wg.Wait()

	// Verify all proposed buckets exist locally
	for i := 0; i < workers; i++ {
		for j := 0; j < opsPerWorker; j++ {
			bucketName := fmt.Sprintf("lin-bucket-%d-%d", i, j)
			if _, err := node1.store.GetBucket(context.Background(), bucketName); err != nil {
				t.Errorf("Linearizability check failed, bucket %s missing: %v", bucketName, err)
			}
		}
	}
}
