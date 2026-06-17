// Package events is an in-process pub/sub bus for domain events surfaced to the UI over SSE.
//
// The bus never back-pressures a publisher: each subscriber has a bounded buffer and a slow
// or full subscriber simply drops events (drop-on-full). This keeps domain paths (approval,
// audit, admin handlers) from stalling on a slow UI client. See ADR-0005.
package events

import (
	"sync"
	"time"
)

// Event is a single domain event fanned out to subscribers.
type Event struct {
	Type string    `json:"type"`
	Data any       `json:"data,omitempty"`
	TS   time.Time `json:"ts"`
}

// Publisher publishes domain events. A nil Publisher is never called by convention; callers
// guard with a nil check (see SetPublisher on approval.Queue / audit.Recorder).
type Publisher interface {
	Publish(eventType string, data any)
}

// subBuffer is the per-subscriber channel capacity. Beyond it, events drop.
const subBuffer = 64

// Bus is a goroutine-safe in-process pub/sub bus implementing Publisher.
type Bus struct {
	mu   sync.Mutex
	subs map[int]chan Event
	next int
}

// NewBus constructs an empty Bus.
func NewBus() *Bus { return &Bus{subs: map[int]chan Event{}} }

// Publish stamps the event with the current time (UTC) and fans it out to every subscriber,
// dropping for any subscriber whose buffer is full. It never blocks.
func (b *Bus) Publish(eventType string, data any) {
	e := Event{Type: eventType, Data: data, TS: time.Now().UTC()}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // subscriber buffer full → drop, never block
		}
	}
}

// Subscribe registers a new subscriber and returns its buffered channel plus a cancel func.
// The cancel func unsubscribes and closes the channel; it is safe to call more than once.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subBuffer)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			close(ch)
			b.mu.Unlock()
		})
	}
	return ch, cancel
}
