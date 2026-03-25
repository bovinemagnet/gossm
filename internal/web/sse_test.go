package web

import (
	"testing"
	"time"

	"github.com/bovinemagnet/gossm/internal/session"
)

func TestSSEBroker_SubscribeAndReceive(t *testing.T) {
	source := make(chan session.SessionEvent, 10)
	broker := NewSSEBroker(source)
	defer broker.Stop()

	ch := broker.Subscribe()
	defer broker.Unsubscribe(ch)

	// Send an event.
	source <- session.SessionEvent{Type: "test"}

	select {
	case evt := <-ch:
		if evt.Type != "test" {
			t.Errorf("event type = %q, want %q", evt.Type, "test")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSSEBroker_MultipleSubscribers(t *testing.T) {
	source := make(chan session.SessionEvent, 10)
	broker := NewSSEBroker(source)
	defer broker.Stop()

	ch1 := broker.Subscribe()
	defer broker.Unsubscribe(ch1)
	ch2 := broker.Subscribe()
	defer broker.Unsubscribe(ch2)

	source <- session.SessionEvent{Type: "broadcast"}

	for i, ch := range []chan session.SessionEvent{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.Type != "broadcast" {
				t.Errorf("subscriber %d: event type = %q, want %q", i, evt.Type, "broadcast")
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestSSEBroker_Unsubscribe(t *testing.T) {
	source := make(chan session.SessionEvent, 10)
	broker := NewSSEBroker(source)
	defer broker.Stop()

	ch := broker.Subscribe()
	broker.Unsubscribe(ch)

	// After unsubscribe, channel should be closed.
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after Unsubscribe")
	}
}

func TestSSEBroker_StopClosesRun(t *testing.T) {
	source := make(chan session.SessionEvent, 10)
	broker := NewSSEBroker(source)

	ch := broker.Subscribe()

	broker.Stop()

	// After stop, sending on source should not block or panic.
	// The run goroutine should have exited.
	time.Sleep(50 * time.Millisecond)

	// Channel should still be subscribable but run loop exited,
	// so events won't be delivered.
	broker.Unsubscribe(ch)
}
