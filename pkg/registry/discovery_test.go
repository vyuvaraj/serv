package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConsulDiscoveryBackend(t *testing.T) {
	// Mock Consul server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == "PUT" && strings.Contains(path, "/v1/agent/service/register") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "PUT" && strings.Contains(path, "/v1/agent/service/deregister") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "GET" && strings.Contains(path, "/v1/catalog/service/") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"ServiceAddress":"127.0.0.1","ServicePort":8080,"ServiceMeta":{"version":"1.0.0"}}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Parse host/port from mock Consul server URL
	address := strings.TrimPrefix(server.URL, "http://")

	backend, err := NewConsulDiscoveryBackend(address)
	if err != nil {
		t.Fatalf("Failed to create Consul backend: %v", err)
	}

	ctx := context.Background()
	err = backend.Register(ctx, "test-service", "127.0.0.1:8080", "1.0.0", "us-east")
	if err != nil {
		t.Errorf("Register failed: %v", err)
	}

	insts, err := backend.Resolve(ctx, "test-service")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if len(insts) != 1 || insts[0].Address != "127.0.0.1:8080" {
		t.Errorf("Expected [127.0.0.1:8080], got %v", insts)
	}

	err = backend.Deregister(ctx, "test-service", "127.0.0.1:8080")
	if err != nil {
		t.Errorf("Deregister failed: %v", err)
	}
}

func TestRegistryWithBackend(t *testing.T) {
	// Custom Mock Backend
	mb := &mockDiscoveryBackend{
		instances: make(map[string][]Instance),
	}

	r := NewRegistry(10 * time.Second)
	r.SetDiscoveryBackend(mb)

	inst := Instance{
		Service: "test-backend",
		Address: "1.2.3.4:9000",
		Version: "2.1.0",
		Region:  "eu-west",
	}

	r.Register(inst)

	res := r.Resolve("test-backend")
	if len(res) != 1 || res[0].Address != "1.2.3.4:9000" {
		t.Errorf("Expected registered instance resolved from backend, got %v", res)
	}
}

type mockDiscoveryBackend struct {
	instances map[string][]Instance
}

func (m *mockDiscoveryBackend) Register(ctx context.Context, service string, addr string, version string, region string) error {
	m.instances[service] = []Instance{{Service: service, Address: addr, Version: version, Region: region}}
	return nil
}

func (m *mockDiscoveryBackend) Deregister(ctx context.Context, service string, addr string) error {
	delete(m.instances, service)
	return nil
}

func (m *mockDiscoveryBackend) Resolve(ctx context.Context, service string) ([]Instance, error) {
	return m.instances[service], nil
}
