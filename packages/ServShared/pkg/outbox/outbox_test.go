package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

type MockBroker struct {
	published []string
	fail      bool
}

func (m *MockBroker) Publish(topic string, payload []byte) error {
	if m.fail {
		return errors.New("network broker connection failure")
	}
	m.published = append(m.published, topic+":"+string(payload))
	return nil
}

func TestOutboxProcessorRelay(t *testing.T) {
	store := NewMemoryOutboxStore()
	broker := &MockBroker{}
	processor := NewOutboxProcessor(store, broker)

	ctx := context.Background()

	// 1. Save events to outbox
	ev1 := OutboxEvent{ID: "evt_1", Topic: "orders", Payload: []byte("order_data_1"), CreatedAt: time.Now()}
	ev2 := OutboxEvent{ID: "evt_2", Topic: "notifications", Payload: []byte("notify_data_2"), CreatedAt: time.Now()}
	store.Save(ctx, ev1)
	store.Save(ctx, ev2)

	// 2. Run processor relay
	count, err := processor.ProcessNext(ctx)
	if err != nil {
		t.Fatalf("processor failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 processed events, got %d", count)
	}

	if len(broker.published) != 2 {
		t.Errorf("expected 2 published broker messages, got %d", len(broker.published))
	}

	// 3. Verify they are marked processed
	unprocessed, _ := store.GetUnprocessed(ctx)
	if len(unprocessed) != 0 {
		t.Errorf("expected 0 unprocessed events left, got %d", len(unprocessed))
	}
}

func TestOutboxProcessorBrokerFailure(t *testing.T) {
	store := NewMemoryOutboxStore()
	broker := &MockBroker{fail: true}
	processor := NewOutboxProcessor(store, broker)

	ctx := context.Background()
	ev := OutboxEvent{ID: "evt_3", Topic: "billing", Payload: []byte("invoice"), CreatedAt: time.Now()}
	store.Save(ctx, ev)

	// Process should fail on broker write
	count, err := processor.ProcessNext(ctx)
	if err == nil {
		t.Error("expected error due to broker failure, got nil")
	}
	if count != 0 {
		t.Errorf("expected 0 processed events, got %d", count)
	}

	// Message remains unprocessed in the outbox
	unprocessed, _ := store.GetUnprocessed(ctx)
	if len(unprocessed) != 1 {
		t.Errorf("expected event to remain in outbox, got count: %d", len(unprocessed))
	}
}
