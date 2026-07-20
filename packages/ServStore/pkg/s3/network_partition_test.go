package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/cluster"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

type partitionableNode struct {
	mu           sync.RWMutex
	store        storage.StorageEngine
	partitioned  bool
}

func (m *partitionableNode) SetPartitioned(val bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.partitioned = val
}

func (m *partitionableNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	isPartitioned := m.partitioned
	m.mu.RUnlock()

	if isPartitioned {
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	bucket, key := parts[0], parts[1]

	if r.Header.Get("X-ServStore-Shard-Index") != "" {
		key = key + ".shard." + r.Header.Get("X-ServStore-Shard-Index")
	}

	if r.Method == "GET" {
		reader, obj, err := m.store.GetObject(r.Context(), bucket, key, "")
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		defer reader.Close()
		w.Header().Set("Content-Type", obj.ContentType)
		w.Header().Set("ETag", obj.ETag)
		w.Header().Set("x-amz-version-id", obj.VersionID)
		if obj.Checksum != "" {
			w.Header().Set("x-amz-meta-blake3", obj.Checksum)
		}
		io.Copy(w, reader)
		return
	}

	if r.Method == "PUT" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, err = m.store.PutObject(r.Context(), bucket, key, bytes.NewReader(data), int64(len(data)), r.Header.Get("Content-Type"))
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

func TestGatewayFailureInjectionNetworkPartition(t *testing.T) {
	dir1, _ := os.MkdirTemp("", "servstore-partition-1-*")
	defer os.RemoveAll(dir1)
	dir2, _ := os.MkdirTemp("", "servstore-partition-2-*")
	defer os.RemoveAll(dir2)

	store1, _ := storage.NewLocalStore(dir1)
	store2, _ := storage.NewLocalStore(dir2)

	ctx := context.Background()
	_ = store1.CreateBucket(ctx, "partition-bucket")
	_ = store2.CreateBucket(ctx, "partition-bucket")

	mockNode2 := &partitionableNode{store: store2}
	srv2 := httptest.NewServer(mockNode2)
	defer srv2.Close()

	addr2 := strings.TrimPrefix(srv2.URL, "http://")

	// Setup membership manager
	mm := cluster.NewMembershipManager("node-1", "localhost:8080", addr2)
	mm.Start(ctx)
	mm.MergeGossip(cluster.GossipPayload{
		SourceNode: cluster.NodeInfo{NodeID: "node-2", Address: addr2, Status: "online", LastSeen: time.Now()},
	})

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store1, authProv, nil, mm, 2, false, 0, 0)

	srv1 := httptest.NewServer(gateway)
	defer srv1.Close()

	client := &http.Client{}

	// --- 1. Put object under normal state (successful replication) ---
	payload := []byte("hello partition test")
	putReq, _ := http.NewRequest("PUT", srv1.URL+"/partition-bucket/item1", bytes.NewReader(payload))
	putReq.SetBasicAuth("admin", "admin")
	putReq.Header.Set("Content-Type", "text/plain")

	resp, err := client.Do(putReq)
	if err != nil {
		t.Fatalf("PUT failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 201/200, got %d", resp.StatusCode)
	}

	// Verify replicated immediately (give it 50ms)
	time.Sleep(50 * time.Millisecond)
	_, _, err = store2.GetObject(ctx, "partition-bucket", "item1", "")
	if err != nil {
		t.Fatalf("Replication to node-2 failed under normal conditions: %v", err)
	}

	// --- 2. Inject Network Partition (node-2 is disconnected/offline) ---
	mockNode2.SetPartitioned(true)

	// Put object while partitioned (local store succeeds, replication logs error)
	putReq2, _ := http.NewRequest("PUT", srv1.URL+"/partition-bucket/item2", bytes.NewReader(payload))
	putReq2.SetBasicAuth("admin", "admin")
	putReq2.Header.Set("Content-Type", "text/plain")

	resp2, err := client.Do(putReq2)
	if err != nil {
		t.Fatalf("PUT under partition failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated && resp2.StatusCode != http.StatusOK {
		t.Fatalf("Expected local PUT to succeed even if partitioned, got %d", resp2.StatusCode)
	}

	// Verify not replicated to node-2
	time.Sleep(50 * time.Millisecond)
	_, _, err = store2.GetObject(ctx, "partition-bucket", "item2", "")
	if err == nil {
		t.Fatal("Expected item2 NOT to be replicated to node-2 under network partition")
	}

	// --- 3. Resolve Network Partition (node-2 comes back online) ---
	mockNode2.SetPartitioned(false)

	// Put new object (normal replication resumes)
	putReq3, _ := http.NewRequest("PUT", srv1.URL+"/partition-bucket/item3", bytes.NewReader(payload))
	putReq3.SetBasicAuth("admin", "admin")
	putReq3.Header.Set("Content-Type", "text/plain")

	resp3, err := client.Do(putReq3)
	if err != nil {
		t.Fatalf("PUT after partition recovery failed: %v", err)
	}
	resp3.Body.Close()

	time.Sleep(50 * time.Millisecond)
	_, _, err = store2.GetObject(ctx, "partition-bucket", "item3", "")
	if err != nil {
		t.Fatalf("Replication failed to resume after partition recovery: %v", err)
	}
}
