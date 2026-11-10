package engine

import "sync"

type Hub struct {
	mu     sync.Mutex
	next   int
	subs   map[int]chan []byte
	closed bool
}

func newHub() *Hub {
	return &Hub{subs: map[int]chan []byte{}}
}

func (h *Hub) subscribe() (int, chan []byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, nil, false
	}
	h.next++
	id := h.next
	ch := make(chan []byte, 16)
	h.subs[id] = ch
	return id, ch, true
}

func (h *Hub) unsubscribe(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
}

func (h *Hub) broadcast(frame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, ch := range h.subs {
		select {
		case ch <- frame:
		default:
			// Slow consumer: drop it instead of blocking the test.
			delete(h.subs, id)
			close(ch)
		}
	}
}

func (h *Hub) close() {
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

func (h *Hub) hasSubscribers() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs) > 0
}
