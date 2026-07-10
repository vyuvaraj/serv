package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"servregistry/pkg/registry"
	"servregistry/pkg/web"
)

func TestProvenanceAndUpstreamMirror(t *testing.T) {
	// Initialize a mock S3 server
	s3Storage := make(map[string][]byte)
	mockS3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == "PUT" {
			data, _ := io.ReadAll(r.Body)
			s3Storage[path] = data
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "GET" {
			if data, ok := s3Storage[path]; ok {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.WriteHeader(http.StatusOK)
				w.Write(data)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == "HEAD" {
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer mockS3.Close()

	// Configure S3 client to use our mock
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL:               mockS3.URL,
			SigningRegion:     "us-east-1",
			HostnameImmutable: true,
		}, nil
	})

	cfg, _ := config.LoadDefaultConfig(context.Background(),
		config.WithEndpointResolverWithOptions(customResolver),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("admin", "admin123", "")),
	)

	registry.S3Client = s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
	})
	defer func() {
		registry.S3Client = nil
		registry.AclStore = nil
	}()

	registry.AclStore = registry.NewACLStore("acls_test.json")
	defer os.Remove("acls_test.json")

	// 1. Test Provenance attestation via publish
	// We'll mock the S3 metadata.json object first
	metaKey := "/serv-packages/testpkg/metadata.json"
	metaData := registry.PackageMetadata{
		Name:     "testpkg",
		Versions: make(map[string]registry.VersionDetails),
	}
	metaBytes, _ := json.Marshal(metaData)
	s3Storage[metaKey] = metaBytes

	// Test the GET /api/v1/packages/provenance/testpkg/1.0.0 endpoint directly:
	provKey := "/serv-packages/testpkg/1.0.0/provenance.json"
	provJson := `{"commit":"commit-123","ci_run_id":"run-456","builder":"github-actions"}`
	s3Storage[provKey] = []byte(provJson)

	reqGet := httptest.NewRequest("GET", "/api/v1/packages/provenance/testpkg/1.0.0", nil)
	wGet := httptest.NewRecorder()
	web.HandleGetProvenance(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Errorf("Expected 200 OK, got %d", wGet.Code)
	}
	var resProv map[string]string
	json.Unmarshal(wGet.Body.Bytes(), &resProv)
	if resProv["commit"] != "commit-123" {
		t.Errorf("Expected commit 'commit-123', got %s", resProv["commit"])
	}

	// 2. Test Upstream Mirror Fallback
	// Setup a mock upstream registry server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "upstream-pkg-1.0.0.tar.gz") {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("upstream-tarball-data"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockUpstream.Close()

	os.Setenv("SERV_UPSTREAM_REGISTRY", mockUpstream.URL)
	defer os.Unsetenv("SERV_UPSTREAM_REGISTRY")

	// Call handleGetPackage for a missing package in our local S3, but present in upstream.
	// Path mapping inside handleGetPackage extracts path from "/packages/".
	// We request "/packages/upstream-pkg/1.0.0/upstream-pkg-1.0.0.tar.gz".
	reqPkg := httptest.NewRequest("GET", "/packages/upstream-pkg/1.0.0/upstream-pkg-1.0.0.tar.gz", nil)
	wPkg := httptest.NewRecorder()
	web.HandleGetPackage(wPkg, reqPkg)

	if wPkg.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from upstream proxy fetch, got %d", wPkg.Code)
	}

	if wPkg.Body.String() != "upstream-tarball-data" {
		t.Errorf("Expected body 'upstream-tarball-data', got '%s'", wPkg.Body.String())
	}

	// Verify it got cached locally in S3!
	cachedKey := "/serv-packages/upstream-pkg/1.0.0/upstream-pkg-1.0.0.tar.gz"
	if cachedData, ok := s3Storage[cachedKey]; !ok || string(cachedData) != "upstream-tarball-data" {
		t.Errorf("Package was not successfully cached in local S3 storage")
	}
}
