package core

import (
	"encoding/json"
	"sync"
)

// Event represents an SSE event to be sent to clients.
type Event struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// EventBroker manages SSE client subscriptions and event broadcasting.
type EventBroker struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

// NewEventBroker creates a new SSE event broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		clients: make(map[chan Event]struct{}),
	}
}

// Subscribe registers a new SSE client and returns a channel to receive events.
func (b *EventBroker) Subscribe() chan Event {
	ch := make(chan Event, 32)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a client.
func (b *EventBroker) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	delete(b.clients, ch)
	close(ch)
	b.mu.Unlock()
}

// Publish sends an event to all connected clients.
func (b *EventBroker) Publish(eventType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	event := Event{Type: eventType, Data: string(data)}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.clients {
		select {
		case ch <- event:
		default:
			// Client too slow, drop the event
		}
	}
}
