package registry

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOCIRegistryStore(t *testing.T) {
	var uploadedBlobs = make(map[string][]byte)
	var uploadedManifests = make(map[string][]byte)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		method := r.Method

		// Ping
		if path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Catalog
		if path == "/v2/_catalog" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"repositories":["testpkg"]}`))
			return
		}

		// Initiate blob upload
		if method == "POST" && strings.HasSuffix(path, "/blobs/uploads/") {
			w.Header().Set("Location", "/v2/testpkg/blobs/uploads/1234")
			w.WriteHeader(http.StatusAccepted)
			return
		}

		// Upload blob bytes
		if method == "PUT" && strings.Contains(path, "/blobs/uploads/1234") {
			digest := r.URL.Query().Get("digest")
			data, _ := io.ReadAll(r.Body)
			uploadedBlobs[digest] = data
			w.WriteHeader(http.StatusCreated)
			return
		}

		// Upload manifest
		if method == "PUT" && strings.Contains(path, "/manifests/") {
			parts := strings.Split(path, "/")
			tag := parts[len(parts)-1]
			data, _ := io.ReadAll(r.Body)
			uploadedManifests[tag] = data
			w.WriteHeader(http.StatusCreated)
			return
		}

		// Get manifest
		if method == "GET" && strings.Contains(path, "/manifests/") {
			parts := strings.Split(path, "/")
			tag := parts[len(parts)-1]
			data, ok := uploadedManifests[tag]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Write(data)
			return
		}

		// Get blob
		if method == "GET" && strings.Contains(path, "/blobs/") {
			parts := strings.Split(path, "/")
			digest := parts[len(parts)-1]
			data, ok := uploadedBlobs[digest]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(data)
			return
		}

		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	store := NewOCIRegistryStore(server.URL, "user", "pass")

	ctx := context.Background()

	// Test PutObject
	testData := []byte("hello-tarball")
	key := "testpkg/testpkg-1.0.0.tar.gz"
	err := store.PutObject(ctx, key, testData)
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	// Verify it was uploaded
	hash := sha256.Sum256(testData)
	digest := fmt.Sprintf("sha256:%x", hash)
	if _, ok := uploadedBlobs[digest]; !ok {
		t.Errorf("Expected blob with digest %s to be uploaded", digest)
	}

	// Test GetObject
	retrievedData, err := store.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}

	if string(retrievedData) != string(testData) {
		t.Errorf("Expected retrieved data %q, got %q", string(testData), string(retrievedData))
	}

	// Test ListObjects
	keys, err := store.ListObjects(ctx)
	if err != nil {
		t.Fatalf("ListObjects failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "testpkg/metadata.json" {
		t.Errorf("Expected [testpkg/metadata.json], got %v", keys)
	}
}
