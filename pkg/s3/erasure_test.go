package s3

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/cluster"
	"servstore/pkg/storage"
)

type mockErasureNode struct {
	store storage.StorageEngine
}

func (m *mockErasureNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		io.Copy(w, reader)
		return
	}

	if r.Method == "PUT" {
		size := r.ContentLength
		contentType := r.Header.Get("Content-Type")
		_, err := m.store.PutObject(r.Context(), bucket, key, r.Body, size, contentType)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}

	if r.Method == "DELETE" {
		_, err := m.store.DeleteObject(r.Context(), bucket, key, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func TestErasureCodingReconstruction(t *testing.T) {
	dir1, _ := os.MkdirTemp("", "servstore-ec-1-*")
	defer os.RemoveAll(dir1)
	dir2, _ := os.MkdirTemp("", "servstore-ec-2-*")
	defer os.RemoveAll(dir2)
	dir3, _ := os.MkdirTemp("", "servstore-ec-3-*")
	defer os.RemoveAll(dir3)

	store1, _ := storage.NewLocalStore(dir1)
	store2, _ := storage.NewLocalStore(dir2)
	store3, _ := storage.NewLocalStore(dir3)

	ctx := context.Background()
	_ = store1.CreateBucket(ctx, "test-bucket")
	_ = store2.CreateBucket(ctx, "test-bucket")
	_ = store3.CreateBucket(ctx, "test-bucket")

	srv2 := httptest.NewServer(&mockErasureNode{store: store2})
	defer srv2.Close()
	srv3 := httptest.NewServer(&mockErasureNode{store: store3})
	defer srv3.Close()

	addr2 := strings.TrimPrefix(srv2.URL, "http://")
	addr3 := strings.TrimPrefix(srv3.URL, "http://")

	mm := cluster.NewMembershipManager("node-1", "localhost:8080", addr2+","+addr3)
	mm.Start(ctx)
	mm.MergeGossip(cluster.GossipPayload{
		SourceNode: cluster.NodeInfo{NodeID: "node-2", Address: addr2, Status: "online", LastSeen: time.Now()},
	})
	mm.MergeGossip(cluster.GossipPayload{
		SourceNode: cluster.NodeInfo{NodeID: "node-3", Address: addr3, Status: "online", LastSeen: time.Now()},
	})

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store1, authProv, nil, mm, 1, true, 2, 1)

	srv1 := httptest.NewServer(gateway)
	defer srv1.Close()

	payload := []byte("highly-durable-erasure-coded-data")
	req, _ := http.NewRequest("PUT", srv1.URL+"/test-bucket/my-file", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	req.SetBasicAuth("admin", "admin")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT returned status %d", resp.StatusCode)
	}

	foundShards := 0
	for i := 0; i < 3; i++ {
		shardName := "my-file.shard." + strconv.Itoa(i)
		if _, err := store1.HeadObject(ctx, "test-bucket", shardName, ""); err == nil {
			foundShards++
		}
		if _, err := store2.HeadObject(ctx, "test-bucket", shardName, ""); err == nil {
			foundShards++
		}
		if _, err := store3.HeadObject(ctx, "test-bucket", shardName, ""); err == nil {
			foundShards++
		}
	}
	if foundShards < 3 {
		t.Errorf("Expected 3 shards to be distributed, found %d", foundShards)
	}

	getReq, _ := http.NewRequest("GET", srv1.URL+"/test-bucket/my-file", nil)
	getReq.SetBasicAuth("admin", "admin")
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	getPayload, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()

	if !bytes.Equal(getPayload, payload) {
		t.Errorf("data mismatch: expected %q, got %q", payload, getPayload)
	}

	mm.MergeGossip(cluster.GossipPayload{
		SourceNode: cluster.NodeInfo{NodeID: "node-3", Address: addr3, Status: "offline", LastSeen: time.Now().Add(-1 * time.Hour)},
	})

	getReq2, _ := http.NewRequest("GET", srv1.URL+"/test-bucket/my-file", nil)
	getReq2.SetBasicAuth("admin", "admin")
	getResp2, err := client.Do(getReq2)
	if err != nil {
		t.Fatalf("GET after node failure failed: %v", err)
	}
	getPayload2, _ := io.ReadAll(getResp2.Body)
	getResp2.Body.Close()

	if !bytes.Equal(getPayload2, payload) {
		t.Errorf("reconstructed data mismatch: expected %q, got %q", payload, getPayload2)
	}

	delReq, _ := http.NewRequest("DELETE", srv1.URL+"/test-bucket/my-file", nil)
	delReq.SetBasicAuth("admin", "admin")
	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE request failed: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE returned status %d", delResp.StatusCode)
	}
}
