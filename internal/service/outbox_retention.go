package service

// Outbox retention (C57T1): prune terminal outbox rows.
//
// Every issue mutation writes one outbox row per subscribed webhook,
// and done/failed rows were never deleted — the table grew
// unboundedly. The sweep below bounds it, following the C13T0
// automation-runs retention precedent (service.PruneAutomationRuns
// + daily ticker.RetentionTicker).
//
// Safety is structural, in the WHERE clause itself — not convention:
//   - status IN ('done','failed') makes deleting pending or
//     delivering rows impossible: pending is undelivered work,
//     delivering rows are the dispatcher's claim leases (lease
//     recovery depends on them).
//   - processed_at IS NOT NULL keeps done/failed rows whose
//     processed_at is somehow unset (markDone/markFailed always set
//     it, but a NULL must never make a row vanish).
// Deletes are batched (OutboxPruneBatchSize per DELETE) so one daily
// pass cannot hold a long lock on a huge outbox; leftovers prune on
// the next pass. The partial index from migration 000063
// (outbox_prune_idx, ON processed_at WHERE status IN
// ('done','failed')) serves the predicate + ORDER BY.
//
// Execution ethos (same as the automation-runs sweep): the sweep
// runs in a scheduled background pass (ticker.OutboxRetentionTicker,
// daily) — never on an HTTP hot path, never inside the enqueue /
// delivery transaction, so a prune failure can never block a
// mutation or a delivery. A failed pass is logged, never fatal.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxRetentionDays is the prune window for terminal (done/failed)
// outbox rows: rows processed longer ago than this are deleted.
// 30 days — shorter than the automation-runs 90: outbox rows are
// operational delivery logs, not audit history. Pinned by test.
const OutboxRetentionDays = 30

// OutboxPruneBatchSize caps the rows one DELETE removes. The pass
// loops until a batch comes back short, so a backlog larger than
// one batch still clears in a single pass, in bounded-lock steps.
const OutboxPruneBatchSize = 1000

// PruneOutbox deletes done/failed outbox rows processed before
// now - OutboxRetentionDays, in batches of OutboxPruneBatchSize.
// The explicit now makes the cutoff deterministic under a test
// clock; production passes time.Now(). Returns the total rows
// deleted.
//
// Pending and delivering rows are never deleted (see the header
// comment); done/failed rows with NULL processed_at are never
// deleted either. A failure surfaces as a returned error for the
// ticker to log — it is never fatal.
func PruneOutbox(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	cutoff := now.AddDate(0, 0, -OutboxRetentionDays)
	var total int64
	for {
		ct, err := pool.Exec(ctx,
			`DELETE FROM outbox WHERE ctid IN (
			   SELECT ctid FROM outbox
			    WHERE status IN ('done', 'failed')
			      AND processed_at IS NOT NULL
			      AND processed_at < $1
			    ORDER BY processed_at
			    LIMIT $2
			 )`,
			cutoff, OutboxPruneBatchSize)
		if err != nil {
			return total, err
		}
		deleted := ct.RowsAffected()
		total += deleted
		if deleted < OutboxPruneBatchSize {
			return total, nil
		}
	}
}
