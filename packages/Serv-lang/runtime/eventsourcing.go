package runtime

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Event represents an event sourcing event payload.
type Event struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Aggregate string                 `json:"aggregate"`
	Payload   map[string]interface{} `json:"payload"`
	Timestamp int64                  `json:"timestamp"`
}

// Projection defines the interface for projecting events into read models.
type Projection interface {
	Handle(event Event) error
}

// EventStore represents a basic event store with projection subscriptions.
type EventStore struct {
	mu          sync.Mutex
	events      []Event
	projections []Projection
}

var (
	defaultEventStore = &EventStore{}
)

// AppendEvent appends a new event and publishes it to all active projections (CORE.9).
func AppendEvent(agg, eventType string, payload map[string]interface{}) Event {
	defaultEventStore.mu.Lock()
	defer defaultEventStore.mu.Unlock()

	ev := Event{
		ID:        fmt.Sprintf("evt-%d", len(defaultEventStore.events)+1),
		Type:      eventType,
		Aggregate: agg,
		Payload:   payload,
		Timestamp: timeNowUnix(),
	}

	defaultEventStore.events = append(defaultEventStore.events, ev)

	// Publish to projections
	for _, proj := range defaultEventStore.projections {
		_ = proj.Handle(ev)
	}

	// Publish to ServQueue topic if broker is active
	payloadStr, _ := json.Marshal(ev)
	Publish("events."+agg, string(payloadStr))

	return ev
}

// RegisterProjection attaches a CQRS read-model projection to the default event stream.
func RegisterProjection(proj Projection) {
	defaultEventStore.mu.Lock()
	defer defaultEventStore.mu.Unlock()
	defaultEventStore.projections = append(defaultEventStore.projections, proj)
}

func timeNowUnix() int64 {
	// Simple clock fallback
	return int64(1777000000)
}
