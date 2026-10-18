package service

// Webhook delivery-history retention (C59T0): prune webhook_deliveries
// rows older than the window.
//
// History lives longer than the outbox queue (90 days vs the outbox's
// 30) so redelivery outlives the queue: the history row carries the
// event + data a redelivery needs, and the outbox row it correlates
// to may already be pruned.
//
// Safety is structural, in the WHERE clause itself — not convention:
// the prune deletes by attempted_at cutoff ONLY. It never deletes
// by status: a 100-day-old retrying row is as prunable as a
// 100-day-old delivered one, and a 10-day-old failed row (still
// redeliverable) is never touched.
//
// Deletes are batched (WebhookDeliveryPruneBatchSize per DELETE) so
// one daily pass cannot hold a long lock on a huge history table;
// leftovers prune on the next pass. Same pattern as PruneOutbox
// (internal/service/outbox_retention.go), same execution ethos:
// the sweep runs in the scheduled background pass (the ticker that
// runs PruneOutbox) — never on an HTTP hot path, never inside the
// enqueue / delivery transaction, so a prune failure can never
// block a mutation or a delivery. A failed pass is logged, never
// fatal.

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WebhookDeliveryRetentionDays is the prune window for delivery
// history rows: rows attempted longer ago than this are deleted.
// 90 days — longer than the outbox's 30, so redelivery outlives the
// queue. Pinned by test.
const WebhookDeliveryRetentionDays = 90

// WebhookDeliveryPruneBatchSize caps the rows one DELETE removes.
// The pass loops until a batch comes back short, so a backlog
// larger than one batch still clears in a single pass, in
// bounded-lock steps.
const WebhookDeliveryPruneBatchSize = 1000

// PruneWebhookDeliveries deletes webhook_deliveries rows attempted
// before now - WebhookDeliveryRetentionDays, in batches of
// WebhookDeliveryPruneBatchSize. The explicit now makes the cutoff
// deterministic under a test clock; production passes time.Now().
// Returns the total rows deleted. A failure surfaces as a returned
// error for the ticker to log — it is never fatal.
func PruneWebhookDeliveries(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int64, error) {
	cutoff := now.AddDate(0, 0, -WebhookDeliveryRetentionDays)
	var total int64
	for {
		ct, err := pool.Exec(ctx,
			`DELETE FROM webhook_deliveries WHERE ctid IN (
			   SELECT ctid FROM webhook_deliveries
			    WHERE attempted_at < $1
			    ORDER BY attempted_at
			    LIMIT $2
			 )`,
			cutoff, WebhookDeliveryPruneBatchSize)
		if err != nil {
			return total, err
		}
		deleted := ct.RowsAffected()
		total += deleted
		if deleted < WebhookDeliveryPruneBatchSize {
			return total, nil
		}
	}
}
