package broker

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
)

type Subscriber struct {
	ID chan string
}

type BrokerEngine struct {
	mu          sync.RWMutex
	topics      map[string][]chan string
	transforms  map[string]wazero.CompiledModule
	wasmManager *WasmManager
}

func NewBrokerEngine() *BrokerEngine {
	// Initialize manager using background context
	mgr, err := GetWasmManager(context.Background())
	if err != nil {
		// Fallback setup or panic in case system/wazero setup fails
		panic(fmt.Sprintf("Failed to initialize WASM Manager: %v", err))
	}

	return &BrokerEngine{
		topics:      make(map[string][]chan string),
		transforms:  make(map[string]wazero.CompiledModule),
		wasmManager: mgr,
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

// RegisterTransform compiles and sets the WASM transform module for a topic
func (e *BrokerEngine) RegisterTransform(ctx context.Context, topic string, wasmBytes []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(wasmBytes) == 0 {
		if compiled, exists := e.transforms[topic]; exists {
			_ = compiled.Close(ctx)
			delete(e.transforms, topic)
		}
		return nil
	}

	compiled, err := e.wasmManager.Compile(ctx, wasmBytes)
	if err != nil {
		return fmt.Errorf("failed to compile WASM module: %w", err)
	}

	// Close old compiled module if one existed
	if old, exists := e.transforms[topic]; exists {
		_ = old.Close(ctx)
	}

	e.transforms[topic] = compiled
	return nil
}

// Publish writes a message to a topic, running any registered WASM transform first
func (e *BrokerEngine) Publish(ctx context.Context, topic string, payload string) (string, error) {
	e.mu.RLock()
	compiledModule, hasTransform := e.transforms[topic]
	e.mu.RUnlock()

	var err error
	processed := payload
	if hasTransform && compiledModule != nil {
		processed, err = e.wasmManager.RunTransform(ctx, compiledModule, payload)
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
