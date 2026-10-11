package mail

import (
	"context"
	"sync"
	"testing"
	"time"
)

// blockingSender blocks inside Send until release is closed, letting a test
// hold a dispatch pass in flight. The mail package shares one outbox table
// with parallel test packages, so a pass may claim other packages' rows;
// Send records every message before blocking so the test can observe
// exactly what each pass started.
type blockingSender struct {
	release chan struct{}
	started chan struct{}
	once    sync.Once
	mu      sync.Mutex
	sent    []Message
}

func (b *blockingSender) Send(ctx context.Context, m Message) error {
	b.mu.Lock()
	b.sent = append(b.sent, m)
	b.mu.Unlock()
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// count returns how many messages have been handed to Send so far. Send
// records the message before it blocks, so this is exactly the set of
// deliveries the in-flight pass has started.
func (b *blockingSender) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sent)
}

// TestDispatcherSingleFlight pins the single-flight guard: a second Run
// that arrives while a pass is still delivering must return immediately
// without claiming or delivering anything. Without the guard, a wedged
// SMTP pass and a 5s ticker would pile up concurrent runs and exhaust the
// pool.
//
// The outbox table is shared with parallel test packages and claimDue
// takes the 25 oldest due rows, so the first pass can claim other
// packages' rows instead of this test's own row (the started signal still
// fires — it only proves a Send was reached, not which row). Every
// assertion below is therefore scoped to explicit signals — the started
// channel, the second run's return, and the recorded send count — never to
// which rows happened to be claimed.
func TestDispatcherSingleFlight(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)

	to := uniqueAddr("singleflight")
	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: to, Subject: "s", Body: "b"})

	sender := &blockingSender{release: make(chan struct{}), started: make(chan struct{})}
	// Always unblock the first pass, even on early failure: without this,
	// a t.Fatal before close(release) would leave the pass — and any
	// piled-up second Run — wedged on the release channel.
	defer func() {
		select {
		case <-sender.release: // already closed on the success path
		default:
			close(sender.release)
		}
	}()
	d := NewDispatcher(pool, sender)

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	select {
	case <-sender.started:
		// The first pass holds inFlight (the CAS precedes Send) and is
		// now blocked inside Send.
	case <-time.After(10 * time.Second):
		t.Fatal("first dispatch pass never reached Send")
	}
	// Send records the message before blocking and the pass is
	// single-threaded, so exactly one delivery has started: the row the
	// pass is blocked on.
	if before := sender.count(); before != 1 {
		t.Fatalf("first pass started %d deliveries, want 1 (blocked in its first Send)", before)
	}

	// The second Run must skip fast instead of claiming rows and blocking
	// behind the in-flight pass. Run it in a goroutine: without the guard
	// it would pile up in Send until release, so the timeout — not a slow
	// return — is the failure signal. 3s is generous for the skip path
	// (one atomic CAS) and matches the sibling webhook single-flight
	// test's precedent.
	run2done := make(chan error, 1)
	go func() { run2done <- d.Run(ctx) }()
	select {
	case err := <-run2done:
		if err != nil {
			t.Fatalf("second Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second Run did not skip while the first pass was in flight")
	}
	// The skipped pass must not have claimed or delivered anything: any
	// claim would have reached Send and been recorded before blocking.
	if got := sender.count(); got != 1 {
		t.Fatalf("second Run started %d deliveries, want 0 (pass must skip without claiming)", got-1)
	}

	close(sender.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("first Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first dispatch pass never finished after release")
	}
	if n := sender.count(); n == 0 {
		t.Error("first pass delivered nothing after release")
	}
}
