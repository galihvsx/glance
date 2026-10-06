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

func readOutboxState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, event, payloadSubstr string) outboxState {
	t.Helper()
	var s outboxState
	err := pool.QueryRow(ctx,
		`SELECT status, attempts, last_error, processed_at IS NOT NULL, next_retry_at
		 FROM outbox WHERE event = $1 AND payload::text LIKE '%'||$2||'%'`, event, payloadSubstr,
	).Scan(&s.status, &s.attempts, &s.lastError, &s.processed, &s.nextRetry)
	if err != nil {
		t.Fatalf("read outbox state: %v", err)
	}
	return s
}

// sentTo returns the delivered messages addressed to recipient. The mail
// dispatcher claims every due email.* row, including rows other parallel
// test packages enqueued — assertions must filter to their own recipient.
func sentTo(sent []Message, recipient string) []Message {
	var out []Message
	for _, m := range sent {
		if m.To == recipient {
			out = append(out, m)
		}
	}
	return out
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
	to := uniqueAddr("user")
	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: to, Subject: "Your login code", Body: "123456"})

	st := readOutboxState(t, ctx, pool, "email.otp", to)
	if st.status != "pending" {
		t.Fatalf("status after enqueue = %q, want %q", st.status, "pending")
	}

	fake := &fakeSender{}
	d := NewDispatcher(pool, fake)
	// Run performs a single pass over at most dispatchBatchSize rows, and
	// rows from other parallel test packages (or earlier runs) share the
	// table — so drain with bounded passes until this test's own message
	// is delivered.
	for i := 0; i < 40 && len(sentTo(fake.sent, to)) == 0; i++ {
		if err := d.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	mine := sentTo(fake.sent, to)
	if len(mine) != 1 {
		t.Fatalf("delivered %d messages to %s, want 1", len(mine), to)
	}
	if mine[0].Subject != "Your login code" {
		t.Errorf("delivered wrong message: %+v", mine[0])
	}

	st = readOutboxState(t, ctx, pool, "email.otp", to)
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

	to := uniqueAddr("retry")
	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: to, Subject: "s", Body: "b"})

	fake := &fakeSender{err: errors.New("smtp down")}
	d := NewDispatcher(pool, fake)

	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("failed send delivered %d messages, want 0", len(fake.sent))
	}
	st := readOutboxState(t, ctx, pool, "email.otp", to)
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
	st = readOutboxState(t, ctx, pool, "email.otp", to)
	if st.attempts != 1 {
		t.Errorf("row was re-claimed during backoff: attempts = %d, want 1", st.attempts)
	}

	// Force the retry due, then deliver for real. Scoped to this test's
	// own row — other packages' rows must not be disturbed.
	if _, err := pool.Exec(ctx, `UPDATE outbox SET next_retry_at = now() WHERE event = 'email.otp' AND payload->>'to' = $1`, to); err != nil {
		t.Fatalf("force retry due: %v", err)
	}
	fake.err = nil
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(sentTo(fake.sent, to)) != 1 {
		t.Fatalf("delivered %d messages to %s after retry, want 1", len(sentTo(fake.sent, to)), to)
	}
	st = readOutboxState(t, ctx, pool, "email.otp", to)
	if st.status != "done" {
		t.Errorf("status after successful retry = %q, want %q", st.status, "done")
	}
}

func TestDispatchMarksFailedAfterMaxAttempts(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	to := uniqueAddr("deadletter")
	enqueueCommitted(t, ctx, pool, "email.otp",
		Message{To: to, Subject: "s", Body: "b"})

	fake := &fakeSender{err: errors.New("permanent smtp failure")}
	d := NewDispatcher(pool, fake)

	// Hammer it: each round forces the backoff due again. After enough
	// failures the row must be marked failed, never retried forever.
	// Scoped to this test's own row — other packages' rows are untouched.
	for i := 0; i < 25; i++ {
		if _, err := pool.Exec(ctx, `UPDATE outbox SET next_retry_at = now() WHERE event = 'email.otp' AND status = 'pending' AND payload->>'to' = $1`, to); err != nil {
			t.Fatalf("force retry due: %v", err)
		}
		if err := d.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
		st := readOutboxState(t, ctx, pool, "email.otp", to)
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
	// must leave it alone. The direct assertion is the webhook row's
	// status: a global "sent nothing" count would be racy, because other
	// parallel test packages legitimately enqueue due email.* rows that
	// this dispatcher run may claim.
	marker := uniqueAddr("webhook-marker")
	if _, err := pool.Exec(ctx,
		`INSERT INTO outbox (event, payload) VALUES ('webhook.delivery', $1)`,
		`{"url":"https://x/`+marker+`"}`); err != nil {
		t.Fatalf("insert webhook row: %v", err)
	}

	fake := &fakeSender{}
	d := NewDispatcher(pool, fake)
	if err := d.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st := readOutboxState(t, ctx, pool, "webhook.delivery", marker)
	if st.status != "pending" {
		t.Errorf("webhook row status = %q, want %q (untouched)", st.status, "pending")
	}
}
