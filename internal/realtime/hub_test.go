package realtime

// Hub unit tests (Task 24): channel fan-out, unsubscribe, and slow-client
// backpressure. No network, no database.

import (
	"testing"
	"time"
)

func recvEvent(t *testing.T, s *Subscriber) Event {
	t.Helper()
	select {
	case e := <-s.send:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func TestHubBroadcastFanout(t *testing.T) {
	h := NewHub()
	s1 := h.AddSubscriber("u1")
	defer h.RemoveSubscriber(s1)
	s2 := h.AddSubscriber("u2")
	defer h.RemoveSubscriber(s2)

	h.Subscribe(s1, "project:acme:ENG")
	h.Subscribe(s1, "workspace:acme")
	h.Subscribe(s2, "workspace:acme")

	h.Broadcast([]string{"project:acme:ENG", "workspace:acme"}, "issue.created",
		map[string]any{"id": "issue-1"})

	// s1 subscribed to both channels → one event per channel.
	e1 := recvEvent(t, s1)
	if e1.Event != "issue.created" || e1.Channel != "project:acme:ENG" {
		t.Fatalf("s1 first = %+v, want issue.created on project:acme:ENG", e1)
	}
	e2 := recvEvent(t, s1)
	if e2.Event != "issue.created" || e2.Channel != "workspace:acme" {
		t.Fatalf("s1 second = %+v, want issue.created on workspace:acme", e2)
	}
	if e1.At.IsZero() {
		t.Fatal("event missing timestamp")
	}
	data, ok := e2.Data.(map[string]any)
	if !ok || data["id"] != "issue-1" {
		t.Fatalf("event data = %#v, want id issue-1", e2.Data)
	}

	// s2 only subscribed to workspace:acme → exactly one event.
	e3 := recvEvent(t, s2)
	if e3.Channel != "workspace:acme" {
		t.Fatalf("s2 = %+v, want workspace:acme", e3)
	}
	select {
	case e := <-s2.send:
		t.Fatalf("s2 got unexpected extra event: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHubUnsubscribe(t *testing.T) {
	h := NewHub()
	s := h.AddSubscriber("u1")
	defer h.RemoveSubscriber(s)

	h.Subscribe(s, "project:acme:ENG")
	h.Unsubscribe(s, "project:acme:ENG")
	h.Broadcast([]string{"project:acme:ENG"}, "issue.created", nil)

	select {
	case e := <-s.send:
		t.Fatalf("got event after unsubscribe: %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHubRemoveSubscriberStopsDelivery(t *testing.T) {
	h := NewHub()
	s := h.AddSubscriber("u1")
	h.Subscribe(s, "project:acme:ENG")
	h.RemoveSubscriber(s)

	// Must not panic or block on a removed subscriber.
	h.Broadcast([]string{"project:acme:ENG"}, "issue.created", nil)
}

func TestHubBroadcastNeverBlocksOnSlowSubscriber(t *testing.T) {
	h := NewHub()
	slow := h.AddSubscriber("slow")
	defer h.RemoveSubscriber(slow)
	h.Subscribe(slow, "project:acme:ENG")

	// Fill the slow subscriber's buffer; nobody ever reads.
	for i := 0; i < sendBufferSize; i++ {
		h.Broadcast([]string{"project:acme:ENG"}, "issue.updated", nil)
	}
	if l := len(slow.send); l != sendBufferSize {
		t.Fatalf("slow buffer = %d, want %d (full)", l, sendBufferSize)
	}

	// Further broadcasts must still return promptly with a full buffer —
	// if Broadcast blocked, the watchdog below fires.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			h.Broadcast([]string{"project:acme:ENG"}, "issue.updated", nil)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Broadcast blocked on a slow subscriber's full buffer")
	}
}

func TestHubDeliversInChannelOrder(t *testing.T) {
	h := NewHub()
	s := h.AddSubscriber("u1")
	defer h.RemoveSubscriber(s)
	h.Subscribe(s, "project:acme:ENG")
	h.Subscribe(s, "workspace:acme")

	// Below the buffer size: nothing drops, and events arrive in the
	// channel order Broadcast was given.
	const n = 10
	for i := 0; i < n; i++ {
		h.Broadcast([]string{"project:acme:ENG", "workspace:acme"}, "issue.updated", map[string]any{"n": i})
	}
	for i := 0; i < n; i++ {
		for _, wantCh := range []string{"project:acme:ENG", "workspace:acme"} {
			select {
			case e := <-s.send:
				if e.Event != "issue.updated" || e.Channel != wantCh {
					t.Fatalf("got %+v, want issue.updated on %s", e, wantCh)
				}
				if e.Data.(map[string]any)["n"] != i {
					t.Fatalf("event %d data = %#v, want n=%d", i, e.Data, i)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for event")
			}
		}
	}
}
