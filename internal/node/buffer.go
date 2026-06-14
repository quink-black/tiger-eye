package node

import (
	"sync"

	"github.com/quink/tiger-eye/internal/event"
)

// buffer is an in-memory ring of recent events with a monotonic per-host seq.
// Pull clients (the collector) read incrementally via since=<seq>. Goroutines
// blocked in events(...) are woken on each append via the broadcast channel.
type buffer struct {
	mu      sync.Mutex
	events  []event.Event // ring; oldest first
	cap     int
	lastSeq uint64

	// notify is closed and replaced on every append so that any number of
	// long-poll waiters can select on a fresh channel without per-waiter state.
	notify chan struct{}
}

func newBuffer(capacity int) *buffer {
	if capacity <= 0 {
		capacity = 1024
	}
	return &buffer{
		events: make([]event.Event, 0, capacity),
		cap:    capacity,
		notify: make(chan struct{}),
	}
}

// append assigns the next seq to e, stores it (evicting the oldest when full),
// and wakes all long-poll waiters. It returns the assigned seq.
func (b *buffer) append(e event.Event) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.lastSeq++
	e.Seq = b.lastSeq
	if len(b.events) >= b.cap {
		copy(b.events, b.events[1:])
		b.events[len(b.events)-1] = e
	} else {
		b.events = append(b.events, e)
	}

	close(b.notify)
	b.notify = make(chan struct{})
	return e.Seq
}

// since returns events with Seq > seq and the current lastSeq. The second
// return is the channel that closes on the next append, for long-polling.
func (b *buffer) since(seq uint64) ([]event.Event, uint64, chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var out []event.Event
	for _, e := range b.events {
		if e.Seq > seq {
			out = append(out, e)
		}
	}
	return out, b.lastSeq, b.notify
}
