package cluster

import (
	"sync"
	"time"
)

type ClusterEvent struct {
	Type      string      `json:"type"`      // "node_join", "node_leave", "node_status_change", "rebalance_progress", "replication_lag"
	Timestamp int64       `json:"timestamp"` // Unix timestamp in ms
	NodeID    string      `json:"node_id,omitempty"`
	Status    string      `json:"status,omitempty"`
	Details   interface{} `json:"details,omitempty"`
}

type EventHub struct {
	mu        sync.RWMutex
	listeners map[chan ClusterEvent]bool
}

var GlobalHub = NewEventHub()

func NewEventHub() *EventHub {
	return &EventHub{
		listeners: make(map[chan ClusterEvent]bool),
	}
}

func (h *EventHub) Subscribe() chan ClusterEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan ClusterEvent, 100)
	h.listeners[ch] = true
	return ch
}

func (h *EventHub) Unsubscribe(ch chan ClusterEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.listeners[ch]; ok {
		delete(h.listeners, ch)
		close(ch)
	}
}

func (h *EventHub) Publish(event ClusterEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixNano() / int64(time.Millisecond)
	}
	for ch := range h.listeners {
		select {
		case ch <- event:
		default:
			// Listener queue full, skip to avoid blocking
		}
	}
}
