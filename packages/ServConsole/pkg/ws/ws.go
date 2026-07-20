package ws

import (
	"fmt"
	"net/http"
	"sync"
)

type EventBroadcaster struct {
	mu      sync.Mutex
	clients map[chan string]bool
}

func NewEventBroadcaster() *EventBroadcaster {
	return &EventBroadcaster{
		clients: make(map[chan string]bool),
	}
}

func (b *EventBroadcaster) Register(ch chan string) {
	b.mu.Lock()
	b.clients[ch] = true
	b.mu.Unlock()
}

func (b *EventBroadcaster) Unregister(ch chan string) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
}

func (b *EventBroadcaster) Broadcast(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.clients {
		select {
		case ch <- event:
		default:
		}
	}
}

func (b *EventBroadcaster) HandleEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan string, 10)
	b.Register(ch)
	defer b.Unregister(ch)

	for {
		select {
		case msg := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", msg)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return
		}
	}
}

func (b *EventBroadcaster) ActiveCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

