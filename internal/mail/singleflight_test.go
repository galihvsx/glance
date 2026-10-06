package mail

import (
	"context"
	"sync"
	"testing"
	"time"
)

// blockingSender blocks inside Send until release is closed, letting a test
// hold a dispatch pass in flight. The mail package shares one outbox table
// with parallel test packages, so assertions filter to the test's own
// recipient instead of counting global deliveries.
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

func (b *blockingSender) countTo(to string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, m := range b.sent {
		if m.To == to {
			n++
		}
	}
	return n
}

// TestDispatcherSingleFlight pins the single-flight guard: a second Run
// that arrives while a pass is still delivering must return immediately
// without claiming or delivering anything. Without the guard, a wedged
// SMTP pass and a 5s ticker would pile up concurrent runs and exhaust the
// pool.
func TestDispatcherSingleFlight(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	to := uniqueAddr("singleflight")
	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: to, Subject: "s", Body: "b"})

	sender := &blockingSender{release: make(chan struct{}), started: make(chan struct{})}
	d := NewDispatcher(pool, sender)

	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	select {
	case <-sender.started:
	case <-time.After(10 * time.Second):
		t.Fatal("first dispatch pass never reached Send")
	}
	// The first pass is blocked in Send with inFlight held. A second Run
	// must skip immediately — not claim, not deliver.
	start := time.Now()
	if err := d.Run(ctx); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("second Run took %v, want immediate skip", elapsed)
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

	if got := sender.countTo(to); got != 1 {
		t.Errorf("delivered %d messages to %s, want exactly 1 (second pass must not re-deliver)", got, to)
	}
	st := readOutboxState(t, ctx, pool, "email.otp", to)
	if st.status != "done" {
		t.Errorf("status = %q, want %q", st.status, "done")
	}
}
