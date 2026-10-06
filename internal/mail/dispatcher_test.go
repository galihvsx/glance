package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeSender records deliveries and optionally fails.
type fakeSender struct {
	sent []Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, m Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

type outboxState struct {
	status    string
	attempts  int
	lastError *string
	processed bool
	nextRetry time.Time
}

func readOutboxState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, event string) outboxState {
	t.Helper()
	var s outboxState
	err := pool.QueryRow(ctx,
		`SELECT status, attempts, last_error, processed_at IS NOT NULL, next_retry_at
		 FROM outbox WHERE event = $1`, event,
	).Scan(&s.status, &s.attempts, &s.lastError, &s.processed, &s.nextRetry)
	if err != nil {
		t.Fatalf("read outbox state: %v", err)
	}
	return s
}

func enqueueCommitted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, event string, msg Message) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := Enqueue(ctx, tx, event, msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestEnqueueAndDispatch(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	// Enqueue inside the caller's transaction; the row must be picked up
	// by the dispatcher after commit.
	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: "user@example.com", Subject: "Your login code", Body: "123456"})

	st := readOutboxState(t, ctx, pool, "email.otp")
	if st.status != "pending" {
		t.Fatalf("status after enqueue = %q, want %q", st.status, "pending")
	}

	fake := &fakeSender{}
	d := NewDispatcher(pool, fake)
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.sent) != 1 {
		t.Fatalf("delivered %d messages, want 1", len(fake.sent))
	}
	if fake.sent[0].To != "user@example.com" || fake.sent[0].Subject != "Your login code" {
		t.Errorf("delivered wrong message: %+v", fake.sent[0])
	}

	st = readOutboxState(t, ctx, pool, "email.otp")
	if st.status != "done" {
		t.Errorf("status after dispatch = %q, want %q", st.status, "done")
	}
	if !st.processed {
		t.Error("processed_at not set after successful dispatch")
	}
	if st.attempts != 0 {
		t.Errorf("attempts = %d, want 0", st.attempts)
	}
}

func TestDispatchRetryBackoff(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: "u@x.com", Subject: "s", Body: "b"})

	fake := &fakeSender{err: errors.New("smtp down")}
	d := NewDispatcher(pool, fake)

	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("failed send delivered %d messages, want 0", len(fake.sent))
	}
	st := readOutboxState(t, ctx, pool, "email.otp")
	if st.status != "pending" {
		t.Errorf("status after transient failure = %q, want %q (retry)", st.status, "pending")
	}
	if st.attempts != 1 {
		t.Errorf("attempts = %d, want 1", st.attempts)
	}
	if st.lastError == nil || *st.lastError == "" {
		t.Error("last_error not recorded")
	}
	if !st.nextRetry.After(time.Now()) {
		t.Errorf("next_retry_at = %v, want in the future (backoff)", st.nextRetry)
	}

	// Immediate re-run must NOT reclaim the row — backoff is pending.
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st = readOutboxState(t, ctx, pool, "email.otp")
	if st.attempts != 1 {
		t.Errorf("row was re-claimed during backoff: attempts = %d, want 1", st.attempts)
	}

	// Force the retry due, then deliver for real.
	if _, err := pool.Exec(ctx, `UPDATE outbox SET next_retry_at = now() WHERE event = 'email.otp'`); err != nil {
		t.Fatalf("force retry due: %v", err)
	}
	fake.err = nil
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.sent) != 1 {
		t.Fatalf("delivered %d messages after retry, want 1", len(fake.sent))
	}
	st = readOutboxState(t, ctx, pool, "email.otp")
	if st.status != "done" {
		t.Errorf("status after successful retry = %q, want %q", st.status, "done")
	}
}

func TestDispatchMarksFailedAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: "u@x.com", Subject: "s", Body: "b"})

	fake := &fakeSender{err: errors.New("permanent smtp failure")}
	d := NewDispatcher(pool, fake)

	// Hammer it: each round forces the backoff due again. After enough
	// failures the row must be marked failed, never retried forever.
	for i := 0; i < 25; i++ {
		if _, err := pool.Exec(ctx, `UPDATE outbox SET next_retry_at = now() WHERE event = 'email.otp' AND status = 'pending'`); err != nil {
			t.Fatalf("force retry due: %v", err)
		}
		if err := d.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
		st := readOutboxState(t, ctx, pool, "email.otp")
		if st.status == "failed" {
			if st.attempts <= 1 {
				t.Errorf("marked failed after only %d attempts", st.attempts)
			}
			return
		}
	}
	t.Fatal("row never marked failed after 25 forced retries")
}

func TestDispatcherIgnoresOtherNamespaces(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	// A webhook.* row belongs to a later dispatcher; the mail dispatcher
	// must leave it alone.
	if _, err := pool.Exec(ctx,
		`INSERT INTO outbox (event, payload) VALUES ('webhook.delivery', '{"url":"https://x"}')`); err != nil {
		t.Fatalf("insert webhook row: %v", err)
	}

	fake := &fakeSender{}
	d := NewDispatcher(pool, fake)
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("mail dispatcher claimed %d non-email rows, want 0", len(fake.sent))
	}
	st := readOutboxState(t, ctx, pool, "webhook.delivery")
	if st.status != "pending" {
		t.Errorf("webhook row status = %q, want %q (untouched)", st.status, "pending")
	}
}
