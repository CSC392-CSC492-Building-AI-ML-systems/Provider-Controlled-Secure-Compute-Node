package dashboard

import (
	"sync"
	"time"
)

// historySize is how many past events a new dashboard connection receives.
const historySize = 100

// Bus fans events out to every connected dashboard and keeps recent history.
// Any part of the agent may Publish; it never blocks on a slow subscriber.
type Bus struct {
	mu      sync.Mutex
	history []Event
	subs    map[chan Event]struct{}
}

func NewBus() *Bus {
	return &Bus{subs: map[chan Event]struct{}{}}
}

// Publish records e and sends it to subscribers. A zero Time is set to now.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.history = append(b.history, e)
	if len(b.history) > historySize {
		b.history = b.history[len(b.history)-historySize:]
	}
	for ch := range b.subs {
		select {
		case ch <- e:
		default: // subscriber is behind; drop rather than stall the agent
		}
	}
}

// Log is a shorthand for Publish.
func (b *Bus) Log(level EventLevel, source, message string) {
	b.Publish(Event{Level: level, Source: source, Message: message})
}

// Subscribe returns the current history and a channel of new events.
// Call cancel when done.
func (b *Bus) Subscribe() (history []Event, events <-chan Event, cancel func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	history = append([]Event(nil), b.history...)
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return history, ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
	}
}
