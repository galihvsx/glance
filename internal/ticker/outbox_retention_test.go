package ticker

// Outbox retention ticker (C57T1): RunOnce delegates to
// service.PruneOutbox against the live clock. The prune logic itself
// (window, safety exclusions, batching) is covered by the
// service-level tests; here we verify the ticker wiring end to end —
// an old done row is gone after one RunOnce, and a second RunOnce
// does not error on an empty backlog.

import (
	"context"
	"testing"
	"time"
)

func TestOutboxRetentionTickerRunOnce(t *testing.T) {
	pool := tickerTestPool(t)
	ctx := context.Background()

	old := time.Now().UTC().AddDate(0, 0, -31)
	var doneOld, pendingOld int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO outbox (event, payload, status, processed_at)
		 VALUES ('test.outbox-retention-ticker', '{}'::jsonb, 'done', $1) RETURNING id`,
		old).Scan(&doneOld); err != nil {
		t.Fatalf("seed old done row: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO outbox (event, payload, status, processed_at)
		 VALUES ('test.outbox-retention-ticker', '{}'::jsonb, 'pending', $1) RETURNING id`,
		old).Scan(&pendingOld); err != nil {
		t.Fatalf("seed old pending row: %v", err)
	}
	// Cleanup: remove the kept pending row — a leftover due row
	// would be visible to later tests in this shared ticker DB.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM outbox WHERE id IN ($1, $2)`, doneOld, pendingOld); err != nil {
			t.Errorf("cleanup outbox rows: %v", err)
		}
	})

	exists := func(id int64) bool {
		var ok bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM outbox WHERE id = $1)`, id).Scan(&ok); err != nil {
			t.Fatalf("row exists: %v", err)
		}
		return ok
	}

	ot := &OutboxRetentionTicker{Pool: pool}
	if err := ot.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if exists(doneOld) {
		t.Fatal("old done row still present after RunOnce, want pruned")
	}
	if !exists(pendingOld) {
		t.Fatal("old pending row pruned by RunOnce, want kept")
	}

	// Second pass over an empty backlog: no error.
	if err := ot.RunOnce(ctx); err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
}
