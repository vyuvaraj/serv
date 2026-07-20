package broker

import (
	"sync"
	"time"
)

type Deduplicator struct {
	mu     sync.Mutex
	ids    map[string]time.Time
	window time.Duration
}

func NewDeduplicator(window time.Duration) *Deduplicator {
	return &Deduplicator{
		ids:    make(map[string]time.Time),
		window: window,
	}
}

func (d *Deduplicator) Add(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	// Clean up expired entries inline
	for k, t := range d.ids {
		if now.Sub(t) > d.window {
			delete(d.ids, k)
		}
	}

	if _, exists := d.ids[id]; exists {
		return false // duplicate
	}

	d.ids[id] = now
	return true
}
