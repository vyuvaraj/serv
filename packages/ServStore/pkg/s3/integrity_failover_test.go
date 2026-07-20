package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/cluster"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestGatewayFailoverOnIntegrityCorruption(t *testing.T) {
	dir1, _ := os.MkdirTemp("", "servstore-failover-1-*")
	defer os.RemoveAll(dir1)
	dir2, _ := os.MkdirTemp("", "servstore-failover-2-*")
	defer os.RemoveAll(dir2)

	store1, _ := storage.NewLocalStore(dir1)
	store2, _ := storage.NewLocalStore(dir2)

	ctx := context.Background()
	_ = store1.CreateBucket(ctx, "failover-bucket")
	_ = store2.CreateBucket(ctx, "failover-bucket")

	// Set up replica node HTTP server
	srv2 := httptest.NewServer(&mockErasureNode{store: store2})
	defer srv2.Close()

	addr2 := strings.TrimPrefix(srv2.URL, "http://")

	// Set up cluster manager. By not specifying peers or setting ring owners manually, 
	// we will manually control replication. But simpler: write directly to store1 and store2 locally.
	// This simulates a successful replication on PUT.
	payload := []byte("important replication test payload data")
	
	// Put to local store1
	ver1, err := store1.PutObject(ctx, "failover-bucket", "test-item", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put to store1: %v", err)
	}

	// Put to local store2 (replicated)
	ver2, err := store2.PutObject(ctx, "failover-bucket", "test-item", bytes.NewReader(payload), int64(len(payload)), "text/plain")
	if err != nil {
		t.Fatalf("failed to put to store2: %v", err)
	}

	// Set up cluster manager so node-1 knows node-2 address for proxying failover
	mm := cluster.NewMembershipManager("node-1", "localhost:8080", addr2)
	mm.Start(ctx)
	mm.MergeGossip(cluster.GossipPayload{
		SourceNode: cluster.NodeInfo{NodeID: "node-2", Address: addr2, Status: "online", LastSeen: time.Now()},
	})

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store1, authProv, nil, mm, 2, false, 0, 0)

	srv1 := httptest.NewServer(gateway)
	defer srv1.Close()

	// Corrupt the file on node-1 (local storage)
	dataDir := filepathJoinLocalStore(dir1, "failover-bucket", ".data")
	dataPath := filepathJoinLocalStore(dataDir, "test-item."+ver1.VersionID)
	err = os.WriteFile(dataPath, []byte("corrupted payload data replacement"), 0644)
	if err != nil {
		t.Fatalf("failed to corrupt local file: %v", err)
	}

	// 3. Request the object via the Gateway's GET endpoint.
	// It should fail verification locally, catch the "integrity corruption detected" error,
	// and automatically failover to node-2 (srv2) which holds the intact copy.
	client := &http.Client{}
	getReq, _ := http.NewRequest("GET", srv1.URL+"/failover-bucket/test-item", nil)
	getReq.SetBasicAuth("admin", "admin")
	
	// Force replica get (normally the ring determines target hosts, but let's make sure
	// it bypasses ring redirect if it's already on node-1, or we set a replication header)
	getReq.Header.Set("X-ServStore-Replicated", "true")

	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failover to succeed and return status 200, got %d", getResp.StatusCode)
	}

	resPayload, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	if !bytes.Equal(resPayload, payload) {
		t.Fatalf("expected original intact payload %q, got %q", payload, resPayload)
	}

	// Check that we got the x-amz-meta-blake3 header
	checksumHeader := getResp.Header.Get("x-amz-meta-blake3")
	if checksumHeader == "" {
		t.Fatalf("expected x-amz-meta-blake3 header to be populated")
	}
	if checksumHeader != ver2.Checksum {
		t.Fatalf("expected checksum %s, got %s", ver2.Checksum, checksumHeader)
	}
}

func filepathJoinLocalStore(elem ...string) string {
	// Simple helper to avoid OS specific slash issues
	return strings.Join(elem, string(os.PathSeparator))
}
