package broker

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

func TestConsumerGroupRebalance(t *testing.T) {
	// Clean WAL file if present to start with a fresh broker
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	engine := NewBrokerEngine()
	defer engine.Stop()

	topic := "rebalance-topic"
	group := "rebalance-group"

	// 1. Subscribe Consumer A and Consumer B to the same consumer group
	chA := engine.SubscribeGroup(topic, group)
	chB := engine.SubscribeGroup(topic, group)

	var receivedA []string
	var receivedB []string
	var mu sync.Mutex

	stopChan := make(chan struct{})

	// Start reading loops for A and B
	go func() {
		for {
			select {
			case msg, ok := <-chA:
				if !ok {
					return
				}
				mu.Lock()
				receivedA = append(receivedA, msg)
				mu.Unlock()
			case <-stopChan:
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case msg, ok := <-chB:
				if !ok {
					return
				}
				mu.Lock()
				receivedB = append(receivedB, msg)
				mu.Unlock()
			case <-stopChan:
				return
			}
		}
	}()

	// Publish 4 messages. They should be round-robined between A and B
	ctx := context.Background()
	for i := 1; i <= 4; i++ {
		_, err := engine.Publish(ctx, topic, fmt.Sprintf("msg-%d", i))
		if err != nil {
			t.Fatalf("failed to publish: %v", err)
		}
	}

	// Wait for message delivery
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	lenA := len(receivedA)
	lenB := len(receivedB)
	mu.Unlock()

	if lenA == 0 || lenB == 0 {
		t.Errorf("expected round-robin delivery: len(A)=%d, len(B)=%d", lenA, lenB)
	}

	// 2. Simulate Consumer A leaving the group (Unsubscribe/disconnect)
	engine.Unsubscribe(topic, chA)

	// Clear collected slices to check new routing
	mu.Lock()
	receivedA = nil
	receivedB = nil
	mu.Unlock()

	// Publish 3 more messages. All should go to B now (rebalanced!)
	for i := 5; i <= 7; i++ {
		_, err := engine.Publish(ctx, topic, fmt.Sprintf("msg-%d", i))
		if err != nil {
			t.Fatalf("failed to publish: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	lenA = len(receivedA)
	lenB = len(receivedB)
	mu.Unlock()

	if lenA != 0 {
		t.Errorf("expected 0 messages delivered to unsubscribed Consumer A, got %d", lenA)
	}
	if lenB != 3 {
		t.Errorf("expected all 3 messages delivered to Consumer B, got %d", lenB)
	}

	// 3. Simulate Consumer C joining the group
	chC := engine.SubscribeGroup(topic, group)
	var receivedC []string

	go func() {
		for {
			select {
			case msg, ok := <-chC:
				if !ok {
					return
				}
				mu.Lock()
				receivedC = append(receivedC, msg)
				mu.Unlock()
			case <-stopChan:
				return
			}
		}
	}()

	// Clear slices again
	mu.Lock()
	receivedB = nil
	receivedC = nil
	mu.Unlock()

	// Publish 4 messages. They should be round-robined between B and C
	for i := 8; i <= 11; i++ {
		_, err := engine.Publish(ctx, topic, fmt.Sprintf("msg-%d", i))
		if err != nil {
			t.Fatalf("failed to publish: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	lenBVal := len(receivedB)
	lenCVal := len(receivedC)
	mu.Unlock()

	if lenBVal == 0 || lenCVal == 0 {
		t.Errorf("expected round-robin delivery to new group members: len(B)=%d, len(C)=%d", lenBVal, lenCVal)
	}

	close(stopChan)
}
