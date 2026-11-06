package engine

import (
	"encoding/json"
	"sync"
)

// Event is pushed to every connected UI over SSE.
type Event struct {
	Seq  int64  `json:"seq"`
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Subscriber receives pre-encoded events. If it falls too far behind the
// channel is closed and the client is expected to reconnect and resync.
type Subscriber struct {
	C      chan []byte
	closed bool
}

type Hub struct {
	mu   sync.Mutex
	seq  int64
	subs map[*Subscriber]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[*Subscriber]struct{}{}} }

func (h *Hub) Subscribe() *Subscriber {
	s := &Subscriber{C: make(chan []byte, 4096)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		if !s.closed {
			s.closed = true
			close(s.C)
		}
	}
}

// Publish encodes the event once and fans it out without blocking.
func (h *Hub) Publish(typ string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) == 0 {
		h.seq++
		return
	}
	h.seq++
	b, err := json.Marshal(Event{Seq: h.seq, Type: typ, Data: data})
	if err != nil {
		return
	}
	for s := range h.subs {
		select {
		case s.C <- b:
		default:
			// Too slow: drop the subscriber so it reconnects with a fresh snapshot.
			delete(h.subs, s)
			s.closed = true
			close(s.C)
		}
	}
}
