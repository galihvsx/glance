package service

// C57T1 outbox retention: service tests. Terminal (done/failed) outbox
// rows older than OutboxRetentionDays are pruned; pending/delivering
// rows are NEVER pruned (pending = undelivered work, delivering = lease
// recovery depends on them), and done/failed rows with a NULL
// processed_at are kept defensively. Deletes are batched so one daily
// pass cannot hold a long lock on a huge outbox.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// insertOutboxRow writes one outbox row with an explicit status /
// processed_at — the dispatcher always stamps processed_at on
// done/failed, so tests backdate via SQL.
func insertOutboxRow(t *testing.T, pool *pgxpool.Pool, status string, processedAt *time.Time) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO outbox (event, payload, status, processed_at)
		 VALUES ('webhook.delivery', '{}'::jsonb, $1, $2) RETURNING id`,
		status, processedAt).Scan(&id); err != nil {
		t.Fatalf("insert outbox row (%s): %v", status, err)
	}
	return id
}

func outboxRowExists(t *testing.T, pool *pgxpool.Pool, id int64) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM outbox WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("outbox row exists: %v", err)
	}
	return exists
}

func TestOutboxRetentionWindowPinned(t *testing.T) {
	// The window is pinned: 30 days, shorter than the automation-runs
	// 90 — outbox rows are operational delivery logs, not audit
	// history.
	if OutboxRetentionDays != 30 {
		t.Fatalf("OutboxRetentionDays = %d, want 30", OutboxRetentionDays)
	}
}

func TestPruneOutboxDeletesOldTerminalRowsOnly(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -OutboxRetentionDays-1)   // prunable
	fresh := now.AddDate(0, 0, -OutboxRetentionDays+1) // inside window: kept
	// A row processed exactly at the cutoff is kept: the predicate is
	// strict (< cutoff), so the boundary belongs to the kept side.
	cutoff := now.AddDate(0, 0, -OutboxRetentionDays)

	// Cleanup: the service package shares one DB and tests run
	// sequentially, so rows this test leaves behind (the KEPT ones)
	// would be visible to later tests — e.g. the dispatcher's global
	// claim would pick up a leftover pending row. Delete every row
	// this test inserted.
	var ids []int64
	t.Cleanup(func() {
		if len(ids) == 0 {
			return
		}
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE id = ANY($1)`, ids); err != nil {
			t.Errorf("cleanup outbox rows: %v", err)
		}
	})
	insert := func(status string, processedAt *time.Time) int64 {
		id := insertOutboxRow(t, pool, status, processedAt)
		ids = append(ids, id)
		return id
	}

	doneOld := insert("done", &old)
	failedOld := insert("failed", &old)
	doneFresh := insert("done", &fresh)
	failedFresh := insert("failed", &fresh)
	doneCutoff := insert("done", &cutoff)
	// THE critical safety rows: undelivered work and leased rows,
	// older than the window, must survive every pass.
	pendingOld := insert("pending", &old)
	deliveringOld := insert("delivering", &old)
	// Defensive rows: done/failed with NULL processed_at must NOT be
	// pruned (markDone/markFailed always set it, but a NULL must never
	// make a row vanish — NULL < cutoff is not true in SQL, and the
	// explicit IS NOT NULL guard says so in the query itself).
	doneNull := insert("done", nil)
	failedNull := insert("failed", nil)
	// Pending/delivering with NULL processed_at are doubly safe.
	pendingNull := insert("pending", nil)

	deleted, err := PruneOutbox(ctx, pool, now)
	if err != nil {
		t.Fatalf("PruneOutbox: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2 (only the old done + failed rows)", deleted)
	}
	for _, tc := range []struct {
		name string
		id   int64
		want bool
	}{
		{"old done pruned", doneOld, false},
		{"old failed pruned", failedOld, false},
		{"fresh done kept", doneFresh, true},
		{"fresh failed kept", failedFresh, true},
		{"cutoff done kept", doneCutoff, true},
		{"old pending KEPT", pendingOld, true},
		{"old delivering KEPT", deliveringOld, true},
		{"NULL-processed_at done KEPT", doneNull, true},
		{"NULL-processed_at failed KEPT", failedNull, true},
		{"NULL-processed_at pending KEPT", pendingNull, true},
	} {
		if got := outboxRowExists(t, pool, tc.id); got != tc.want {
			t.Fatalf("%s: exists = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPruneOutboxBatching(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -OutboxRetentionDays-1)

	// More than one batch of prunable rows in a single pass: the
	// delete loops until the batch comes back short, so one call
	// clears the whole backlog without ever holding one giant lock.
	// A kept row proves the batch loop does not over-delete.
	// The batch rows carry a unique event so the count below is
	// scoped to this test (the service package shares one DB).
	total := OutboxPruneBatchSize*2 + 7
	if _, err := pool.Exec(ctx,
		`INSERT INTO outbox (event, payload, status, processed_at)
		 SELECT 'test.outbox-retention-batch', '{}'::jsonb, 'done', $1
		   FROM generate_series(1, $2)`,
		old, total); err != nil {
		t.Fatalf("seed batch rows: %v", err)
	}
	kept := insertOutboxRow(t, pool, "pending", &old)
	// Cleanup: remove the kept pending row — a leftover due row
	// would be visible to the dispatcher's global claim in later
	// tests. (The batch rows are all pruned by the pass itself.)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE id = $1`, kept); err != nil {
			t.Errorf("cleanup kept outbox row: %v", err)
		}
	})

	deleted, err := PruneOutbox(ctx, pool, now)
	if err != nil {
		t.Fatalf("PruneOutbox (batched): %v", err)
	}
	if deleted < int64(total) {
		t.Fatalf("deleted = %d, want at least %d (all batches across passes)", deleted, total)
	}
	if !outboxRowExists(t, pool, kept) {
		t.Fatal("pending row deleted by batched prune, want kept")
	}
	var batchLeft int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM outbox WHERE event = 'test.outbox-retention-batch'`).Scan(&batchLeft); err != nil {
		t.Fatalf("count batch rows: %v", err)
	}
	if batchLeft != 0 {
		t.Fatalf("remaining batch rows = %d, want 0", batchLeft)
	}

	// A second pass over an empty backlog deletes nothing.
	deleted, err = PruneOutbox(ctx, pool, now)
	if err != nil {
		t.Fatalf("PruneOutbox (second pass): %v", err)
	}
	if deleted != 0 {
		t.Fatalf("second pass deleted = %d, want 0", deleted)
	}
}

func TestPruneOutboxFailureIsNeverFatal(t *testing.T) {
	// A sweep failure surfaces as a returned error — no panic, no
	// process death. The ticker logs the error and keeps ticking, so a
	// broken pass is never fatal to the server.
	pool := newTestPool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PruneOutbox(ctx, pool, time.Now()); err == nil {
		t.Fatal("PruneOutbox with cancelled context: expected error, got nil")
	}
}
