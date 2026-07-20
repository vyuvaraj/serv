package edge

import (
	"bytes"
	"strings"
	"testing"
)

func TestEdgeRuntimeRoutingAndSync(t *testing.T) {
	rt := NewEdgeRuntime()

	// 1. Register WASM function
	rt.RegisterWASMHandler("uppercase", func(payload string) (string, error) {
		return strings.ToUpper(payload), nil
	})

	// 2. Test Geo-Routing
	nodeUS, err := rt.RouteGeo("US")
	if err != nil || nodeUS.Region != "us-east" {
		t.Errorf("expected US route to us-east, got %v, err=%v", nodeUS, err)
	}

	nodeAS, err := rt.RouteGeo("Asia")
	if err != nil || nodeAS.Region != "ap-south" {
		t.Errorf("expected Asia route to ap-south, got %v, err=%v", nodeAS, err)
	}

	// 3. Run WASM on a region
	res, err := rt.ExecuteWASM("eu-west", "uppercase", "hello edge")
	if err != nil || res != "HELLO EDGE" {
		t.Errorf("WASM execution failed: res=%s, err=%v", res, err)
	}

	// 4. Test Offline Sync replication
	err = rt.WriteEdge("ap-south", "sensor_123", []byte("ambient_temp:22"))
	if err != nil {
		t.Fatalf("WriteEdge failed: %v", err)
	}

	// Not synced to primary yet
	_, exists := rt.ReadPrimary("sensor_123")
	if exists {
		t.Error("expected data to not be present in primary yet")
	}

	// Flush queues (online sync)
	flushed := rt.FlushSyncQueues()
	if flushed != 1 {
		t.Errorf("expected 1 record synced, got %d", flushed)
	}

	val, exists := rt.ReadPrimary("sensor_123")
	if !exists || !bytes.Equal(val, []byte("ambient_temp:22")) {
		t.Errorf("primary DB sync mismatch: val=%s", string(val))
	}
}

func TestEdgeRuntimeExecutionHostBoundary(t *testing.T) {
	rt := NewEdgeRuntime()

	if err := rt.VerifyExecutionHost("secure-enclave-node"); err != nil {
		t.Errorf("expected secure-enclave-node to be valid: %v", err)
	}

	if err := rt.VerifyExecutionHost("public-untrusted-node"); err == nil {
		t.Error("expected public-untrusted-node to fail security boundary verification")
	}
}
