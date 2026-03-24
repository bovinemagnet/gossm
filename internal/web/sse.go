package web

import (
	"sync"

	"github.com/bovinemagnet/gossm/internal/session"
)

// SSEBroker fans out SessionEvents to connected browser clients.
type SSEBroker struct {
	mu      sync.Mutex
	clients map[chan session.SessionEvent]struct{}
	source  chan session.SessionEvent
	done    chan struct{}
}

// NewSSEBroker creates a broker that reads events from the onChange channel
// and distributes them to all subscribed clients.
func NewSSEBroker(onChange chan session.SessionEvent) *SSEBroker {
	b := &SSEBroker{
		clients: make(map[chan session.SessionEvent]struct{}),
		source:  onChange,
		done:    make(chan struct{}),
	}
	go b.run()
	return b
}

// run reads from the source channel and fans out to all subscribed clients.
func (b *SSEBroker) run() {
	for {
		select {
		case <-b.done:
			return
		case evt, ok := <-b.source:
			if !ok {
				return
			}
			b.mu.Lock()
			for ch := range b.clients {
				select {
				case ch <- evt:
				default:
					// Drop if the client is too slow.
				}
			}
			b.mu.Unlock()
		}
	}
}

// Subscribe adds a new client channel and returns it.
func (b *SSEBroker) Subscribe() chan session.SessionEvent {
	ch := make(chan session.SessionEvent, 16)
	b.mu.Lock()
	b.clients[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel and closes it.
func (b *SSEBroker) Unsubscribe(ch chan session.SessionEvent) {
	b.mu.Lock()
	delete(b.clients, ch)
	b.mu.Unlock()
	close(ch)
}

// Stop shuts down the broker.
func (b *SSEBroker) Stop() {
	close(b.done)
}
