package broker

import (
	"context"
	"sync"
)

type Subscriber struct {
	ID chan string
}

type BrokerEngine struct {
	mu           sync.RWMutex
	topics       map[string][]chan string
	transforms   map[string][]byte
}

func NewBrokerEngine() *BrokerEngine {
	return &BrokerEngine{
		topics:     make(map[string][]chan string),
		transforms: make(map[string][]byte),
	}
}

// Subscribe adds a subscriber channel to a topic
func (e *BrokerEngine) Subscribe(topic string) chan string {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := make(chan string, 100)
	e.topics[topic] = append(e.topics[topic], ch)
	return ch
}

// Unsubscribe removes a subscriber channel from a topic
func (e *BrokerEngine) Unsubscribe(topic string, ch chan string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	subs, exists := e.topics[topic]
	if !exists {
		return
	}

	for i, sub := range subs {
		if sub == ch {
			e.topics[topic] = append(subs[:i], subs[i+1:]...)
			close(ch)
			break
		}
	}
}

// RegisterTransform sets the WASM transform module bytes for a topic
func (e *BrokerEngine) RegisterTransform(topic string, wasmBytes []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if wasmBytes == nil {
		delete(e.transforms, topic)
	} else {
		e.transforms[topic] = wasmBytes
	}
}

// Publish writes a message to a topic, running any registered WASM transform first
func (e *BrokerEngine) Publish(ctx context.Context, topic string, payload string) (string, error) {
	e.mu.RLock()
	wasmBytes, hasTransform := e.transforms[topic]
	e.mu.RUnlock()

	var err error
	processed := payload
	if hasTransform && len(wasmBytes) > 0 {
		processed, err = RunTransform(ctx, wasmBytes, payload)
		if err != nil {
			return payload, err
		}
	}

	e.mu.RLock()
	subs, exists := e.topics[topic]
	e.mu.RUnlock()

	if exists {
		for _, sub := range subs {
			select {
			case sub <- processed:
			default:
				// Skip if channel buffer is full to prevent blocking the publisher
			}
		}
	}

	return processed, nil
}
