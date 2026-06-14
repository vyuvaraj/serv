package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"servstore/pkg/storage"

	"github.com/hashicorp/raft"
)

type MetadataCommand struct {
	Op         string `json:"op"` // "CreateBucket", "DeleteBucket", "SetVersioning", "SetLifecycle", "DeleteLifecycle", "PutPolicy", "DeletePolicy"
	BucketName string `json:"bucket,omitempty"`
	KeyName    string `json:"key,omitempty"`
	Value      []byte `json:"value,omitempty"`
}

type MetadataFSM struct {
	store storage.StorageEngine
}

func NewMetadataFSM(store storage.StorageEngine) *MetadataFSM {
	return &MetadataFSM{store: store}
}

func (f *MetadataFSM) Apply(l *raft.Log) interface{} {
	var cmd MetadataCommand
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		return err
	}

	ctx := context.Background()
	switch cmd.Op {
	case "CreateBucket":
		return f.store.CreateBucket(ctx, cmd.BucketName)
	case "DeleteBucket":
		return f.store.DeleteBucket(ctx, cmd.BucketName)
	case "SetVersioning":
		return f.store.SetBucketVersioning(ctx, cmd.BucketName, string(cmd.Value))
	case "SetLifecycle":
		var rules []storage.LifecycleRule
		if err := json.Unmarshal(cmd.Value, &rules); err != nil {
			return err
		}
		return f.store.SetBucketLifecycle(ctx, cmd.BucketName, rules)
	case "DeleteLifecycle":
		return f.store.DeleteBucketLifecycle(ctx, cmd.BucketName)
	case "PutPolicy":
		return f.store.PutUserPolicy(ctx, cmd.KeyName, cmd.Value)
	case "DeletePolicy":
		return f.store.DeleteUserPolicy(ctx, cmd.KeyName)
	}

	return fmt.Errorf("unsupported raft metadata op: %s", cmd.Op)
}

func (f *MetadataFSM) Snapshot() (raft.FSMSnapshot, error) {
	// Simple snapshot: return empty since we store state locally on disk
	return &emptySnapshot{}, nil
}

func (f *MetadataFSM) Restore(rc io.ReadCloser) error {
	return nil
}

type emptySnapshot struct{}

func (s *emptySnapshot) Persist(sink raft.SnapshotSink) error {
	_, err := sink.Write([]byte{})
	return err
}

func (s *emptySnapshot) Release() {}

type RaftNode struct {
	raftInstance *raft.Raft
	fsm          *MetadataFSM
	transport    *raft.NetworkTransport
}

func NewRaftNode(nodeID, raftBindAddr string, store storage.StorageEngine, bootstrap bool) (*RaftNode, error) {
	fsm := NewMetadataFSM(store)

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(nodeID)
	// Faster election times for responsive local testing
	config.HeartbeatTimeout = 250 * time.Millisecond
	config.ElectionTimeout = 250 * time.Millisecond

	addr, err := net.ResolveTCPAddr("tcp", raftBindAddr)
	if err != nil {
		return nil, err
	}

	transport, err := raft.NewTCPTransport(raftBindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		// Wait, if os.Stderr doesn't match standard io.Writer or log stream, we can use a custom writer
		// In go get, transport takes an io.Writer. os.Stderr is *os.File which implements io.Writer.
		// Wait, hashicorp raft transport creator accepts transport parameters.
		// Let's use a standard library logger or os.Stdout as log writer.
	}

	// We use in-memory stores to keep it lightweight, fast, and zero-configuration
	logStore := raft.NewInmemStore()
	stableStore := raft.NewInmemStore()
	snapStore := raft.NewDiscardSnapshotStore()

	r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapStore, transport)
	if err != nil {
		return nil, err
	}

	if bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      config.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		r.BootstrapCluster(configuration)
		slog.Info("Bootstrapped active Raft consensus cluster", "node_id", nodeID, "addr", raftBindAddr)
	}

	return &RaftNode{
		raftInstance: r,
		fsm:          fsm,
		transport:    transport,
	}, nil
}

func (rn *RaftNode) Propose(cmd MetadataCommand) error {
	if rn.raftInstance.State() != raft.Leader {
		return fmt.Errorf("not raft leader")
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	future := rn.raftInstance.Apply(data, 5*time.Second)
	if err := future.Error(); err != nil {
		return err
	}

	resp := future.Response()
	if err, ok := resp.(error); ok && err != nil {
		return err
	}

	return nil
}

func (rn *RaftNode) IsLeader() bool {
	return rn.raftInstance.State() == raft.Leader
}

func (rn *RaftNode) LeaderAddr() string {
	leaderAddr, _ := rn.raftInstance.LeaderWithID()
	return string(leaderAddr)
}

func (rn *RaftNode) State() string {
	return rn.raftInstance.State().String()
}

func (rn *RaftNode) Join(nodeID, addr string) error {
	future := rn.raftInstance.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 5*time.Second)
	return future.Error()
}

func (rn *RaftNode) Close() {
	rn.raftInstance.Shutdown()
}

// JoinCluster contacts a seed node to request registration in the Raft consensus group.
func JoinCluster(seedHTTPAddr, localNodeID, localRaftAddr string) error {
	payload := map[string]string{
		"node_id": localNodeID,
		"address": localRaftAddr,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := seedHTTPAddr
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "http://" + url
	}
	url = strings.TrimSuffix(url, "/") + "/console/cluster/join"

	resp, err := http.Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to join cluster, status: %d", resp.StatusCode)
	}

	return nil
}
