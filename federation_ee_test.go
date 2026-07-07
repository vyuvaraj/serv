//go:build enterprise

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"servqueue/pkg/broker"
	"servqueue/pkg/web"
)

// TestEventBusFederation verifies that publishing on engine1 causes the event to be
// mirrored to engine2 via the federation HTTP target. This test only runs under the
// enterprise build tag because cross-cluster federation is an EE-only feature.
func TestEventBusFederation(t *testing.T) {
	engine2 := broker.NewBrokerEngine()
	defer engine2.Stop()
	server2 := web.NewServer("127.0.0.1:8088", engine2, "gateway-secret-token", "", "")
	go server2.Start()
	defer server2.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)

	engine1 := broker.NewBrokerEngine()
	defer engine1.Stop()

	// Subscribe to target before publish
	subChan := engine2.Subscribe("my-topic.federated")

	os.Setenv("SERVQUEUE_FEDERATION_TARGET", "http://127.0.0.1:8088")
	defer os.Unsetenv("SERVQUEUE_FEDERATION_TARGET")

	_, err := engine1.Publish(context.Background(), "my-topic.federated", "hello-federated-event")
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	select {
	case received := <-subChan:
		if received != "hello-federated-event" {
			t.Errorf("Expected 'hello-federated-event', got %q", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for mirrored federated event")
	}
}
