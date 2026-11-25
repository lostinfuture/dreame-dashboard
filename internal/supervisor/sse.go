package supervisor

import (
	"sync"
)

// Event is one server-sent event.
type Event struct {
	Name string
	Data string
}

// Hub fans events out to a bounded set of subscribers.
type Hub struct {
	mu     sync.Mutex
	subs   map[int]chan Event
	next   int
	max    int
	closed bool
}

// NewHub returns a hub that keeps at most 16 subscribers.
func NewHub() *Hub {
	return &Hub{subs: map[int]chan Event{}, max: 16}
}

// Subscribe registers a client. The cancel func removes it.
// When the hub is full the oldest subscriber is dropped.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	if h.max == 0 {
		h.max = 16
	}
	for len(h.subs) >= h.max {
		var oldest int
		first := true
		for id := range h.subs {
			if first || id < oldest {
				oldest = id
				first = false
			}
		}
		close(h.subs[oldest])
		delete(h.subs, oldest)
	}
	id := h.next
	h.next++
	ch := make(chan Event, 8)
	h.subs[id] = ch
	return ch, func() { h.drop(id) }
}

func (h *Hub) drop(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch, ok := h.subs[id]
	if !ok {
		return
	}
	delete(h.subs, id)
	close(ch)
}

// Publish sends an event. A subscriber that cannot accept it is dropped.
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			delete(h.subs, id)
			close(ch)
		}
	}
}

// Close drops every subscriber so SSE handlers return.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, ch := range h.subs {
		delete(h.subs, id)
		close(ch)
	}
}

// Len is the subscriber count.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
