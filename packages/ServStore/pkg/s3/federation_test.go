//go:build enterprise

package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestE2EFederation(t *testing.T) {
	// 1. Initialize and start Cluster 2 (Remote/Target Cluster)
	dir2 := t.TempDir()
	store2, err := storage.NewLocalStore(dir2)
	if err != nil {
		t.Fatalf("failed to create store 2: %v", err)
	}
	defer store2.Close()

	auth2 := auth.NewAuthProvider("", "", false)
	gateway2 := NewGateway(store2, auth2, nil, nil, 1, false, 0, 0)

	server2 := httptest.NewServer(gateway2)
	defer server2.Close()

	// 2. Initialize and start Cluster 1 (Local Gateway)
	dir1 := t.TempDir()
	store1, err := storage.NewLocalStore(dir1)
	if err != nil {
		t.Fatalf("failed to create store 1: %v", err)
	}
	defer store1.Close()

	auth1 := auth.NewAuthProvider("", "", false)
	gateway1 := NewGateway(store1, auth1, nil, nil, 1, false, 0, 0)

	// Configure federation rule on Gateway 1: "east-*" goes to Gateway 2
	gateway1.fedMutex.Lock()
	gateway1.federationRules = append(gateway1.federationRules, FederationRule{
		Pattern: "east-*",
		Target:  server2.URL,
	})
	gateway1.fedMutex.Unlock()

	server1 := httptest.NewServer(gateway1)
	defer server1.Close()

	// 3. Send S3 requests to Cluster 1 targeting bucket "east-bucket"
	client := &http.Client{}

	// A. Create Bucket "east-bucket" on Cluster 1
	reqCreate, err := http.NewRequest("PUT", fmt.Sprintf("%s/east-bucket", server1.URL), nil)
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	respCreate, err := client.Do(reqCreate)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	if respCreate.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", respCreate.StatusCode)
	}
	respCreate.Body.Close()

	// B. Put Object "east-bucket/document.txt" on Cluster 1
	content := []byte("hello-from-the-east-cluster")
	reqPut, err := http.NewRequest("PUT", fmt.Sprintf("%s/east-bucket/document.txt", server1.URL), bytes.NewReader(content))
	if err != nil {
		t.Fatalf("failed to create PUT object request: %v", err)
	}
	reqPut.Header.Set("Content-Type", "text/plain")
	respPut, err := client.Do(reqPut)
	if err != nil {
		t.Fatalf("PUT object failed: %v", err)
	}
	if respPut.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", respPut.StatusCode)
	}
	respPut.Body.Close()

	// C. Get Object "east-bucket/document.txt" from Cluster 1
	reqGet, err := http.NewRequest("GET", fmt.Sprintf("%s/east-bucket/document.txt", server1.URL), nil)
	if err != nil {
		t.Fatalf("failed to create GET object request: %v", err)
	}
	respGet, err := client.Do(reqGet)
	if err != nil {
		t.Fatalf("GET object failed: %v", err)
	}
	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", respGet.StatusCode)
	}
	data, _ := io.ReadAll(respGet.Body)
	respGet.Body.Close()

	if string(data) != "hello-from-the-east-cluster" {
		t.Errorf("expected 'hello-from-the-east-cluster', got %q", string(data))
	}

	// 4. Verify physical location:
	// - Bucket "east-bucket" must exist in store 2 (Remote).
	// - Bucket "east-bucket" must NOT exist in store 1 (Local).
	_, err = store2.GetBucket(context.Background(), "east-bucket")
	if err != nil {
		t.Errorf("expected bucket to exist on cluster 2: %v", err)
	}

	_, err = store1.GetBucket(context.Background(), "east-bucket")
	if err == nil {
		t.Errorf("expected bucket to NOT exist on cluster 1 (should have been transparently federated)")
	}
}

func TestE2EGlobalNamespace(t *testing.T) {
	// 1. Initialize Cluster 2 (eu-west node)
	dir2 := t.TempDir()
	store2, err := storage.NewLocalStore(dir2)
	if err != nil {
		t.Fatalf("failed to create store 2: %v", err)
	}
	defer store2.Close()
	auth2 := auth.NewAuthProvider("", "", false)
	gateway2 := NewGateway(store2, auth2, nil, nil, 1, false, 0, 0)
	server2 := httptest.NewServer(gateway2)
	defer server2.Close()

	// 2. Initialize Cluster 1 (local node)
	dir1 := t.TempDir()
	store1, err := storage.NewLocalStore(dir1)
	if err != nil {
		t.Fatalf("failed to create store 1: %v", err)
	}
	defer store1.Close()
	auth1 := auth.NewAuthProvider("", "", false)
	gateway1 := NewGateway(store1, auth1, nil, nil, 1, false, 0, 0)

	// Configure federation rule on Gateway 1: "eu-west" region pattern -> server2
	gateway1.fedMutex.Lock()
	gateway1.federationRules = append(gateway1.federationRules, FederationRule{
		Pattern: "eu-west",
		Target:  server2.URL,
	})
	gateway1.fedMutex.Unlock()

	server1 := httptest.NewServer(gateway1)
	defer server1.Close()

	client := &http.Client{}

	// A. Create Bucket "mybucket@eu-west" on Cluster 1 -> should go to Cluster 2
	reqCreate, err := http.NewRequest("PUT", fmt.Sprintf("%s/mybucket@eu-west", server1.URL), nil)
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	respCreate, err := client.Do(reqCreate)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	if respCreate.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", respCreate.StatusCode)
	}
	respCreate.Body.Close()

	// Check that bucket resides on Cluster 2 as "mybucket" (stripped name)
	_, err = store2.GetBucket(context.Background(), "mybucket")
	if err != nil {
		t.Errorf("expected bucket to exist on cluster 2: %v", err)
	}

	// B. Create Bucket "localbucket@us-east" on Cluster 1 (processed locally since no federation rule for us-east)
	reqCreateLocal, err := http.NewRequest("PUT", fmt.Sprintf("%s/localbucket@us-east", server1.URL), nil)
	if err != nil {
		t.Fatalf("failed to create PUT request: %v", err)
	}
	respCreateLocal, err := client.Do(reqCreateLocal)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	if respCreateLocal.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", respCreateLocal.StatusCode)
	}
	respCreateLocal.Body.Close()

	// Check that bucket resides on Cluster 1 as "localbucket" (stripped name)
	_, err = store1.GetBucket(context.Background(), "localbucket")
	if err != nil {
		t.Errorf("expected bucket to exist on cluster 1: %v", err)
	}
}
