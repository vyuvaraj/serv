package outbox

import (
	"context"
	"errors"
	"sync"
	"time"
)

type OutboxEvent struct {
	ID        string
	Topic     string
	Payload   []byte
	CreatedAt time.Time
	Processed bool
}

type OutboxStore interface {
	Save(ctx context.Context, event OutboxEvent) error
	GetUnprocessed(ctx context.Context) ([]OutboxEvent, error)
	MarkProcessed(ctx context.Context, id string) error
}

type MemoryOutboxStore struct {
	mu     sync.Mutex
	events []OutboxEvent
}

func NewMemoryOutboxStore() *MemoryOutboxStore {
	return &MemoryOutboxStore{
		events: make([]OutboxEvent, 0),
	}
}

func (m *MemoryOutboxStore) Save(ctx context.Context, event OutboxEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *MemoryOutboxStore) GetUnprocessed(ctx context.Context) ([]OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []OutboxEvent
	for _, e := range m.events {
		if !e.Processed {
			list = append(list, e)
		}
	}
	return list, nil
}

func (m *MemoryOutboxStore) MarkProcessed(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, e := range m.events {
		if e.ID == id {
			m.events[i].Processed = true
			return nil
		}
	}
	return errors.New("event not found")
}

type Broker interface {
	Publish(topic string, payload []byte) error
}

type OutboxProcessor struct {
	store  OutboxStore
	broker Broker
}

func NewOutboxProcessor(store OutboxStore, broker Broker) *OutboxProcessor {
	return &OutboxProcessor{
		store:  store,
		broker: broker,
	}
}

// ProcessNext fetches unprocessed events and relays them via the broker
func (o *OutboxProcessor) ProcessNext(ctx context.Context) (int, error) {
	events, err := o.store.GetUnprocessed(ctx)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, ev := range events {
		if err := o.broker.Publish(ev.Topic, ev.Payload); err != nil {
			// Stop processing further if broker is down/unreachable
			return count, err
		}
		if err := o.store.MarkProcessed(ctx, ev.ID); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
