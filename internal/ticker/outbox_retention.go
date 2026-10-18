// Outbox retention (C57T1) + webhook delivery-history retention
// (C59T0).
//
// Once a day the retention pass deletes done/failed outbox rows
// processed longer ago than the retention window (30 days,
// service.OutboxRetentionDays). Pending and delivering rows are
// never touched — the safety lives in service.PruneOutbox's WHERE
// clause, not here. The same pass also deletes webhook_deliveries
// rows attempted longer ago than 90 days
// (service.WebhookDeliveryRetentionDays) — history outlives the
// queue so redelivery keeps working after the outbox row is gone.
// The prune logic itself lives in service.PruneOutbox /
// service.PruneWebhookDeliveries; this file is only the interval
// loop.
//
// Same ethos as the automation-run retention: no HTTP hot path,
// never inside the enqueue/delivery transaction, so a prune
// failure can never block a mutation or a delivery. A failed pass
// is logged (log.Printf here) and the ticker keeps ticking — never
// fatal.
//
// In-process by design (same known limitation as the cycle, reminder,
// stale-nudge, and digest tickers, spec §3): one instance only in v1.
package ticker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// OutboxRetentionTicker runs the daily outbox retention prune.
// Interval <= 0 means the default (24 hours).
type OutboxRetentionTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one retention pass against the live clock: the
// outbox prune, then the delivery-history prune. Both sweeps run
// even if the first fails — a broken outbox sweep must not starve
// history retention (or vice versa) — and the first error is the
// one returned for the ticker to log.
func (t *OutboxRetentionTicker) RunOnce(ctx context.Context) error {
	_, outboxErr := service.PruneOutbox(ctx, t.Pool, time.Now())
	_, historyErr := service.PruneWebhookDeliveries(ctx, t.Pool, time.Now())
	if outboxErr != nil {
		return outboxErr
	}
	return historyErr
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once.
func (t *OutboxRetentionTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		log.Printf("glance: outbox-retention ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx); err != nil {
					log.Printf("glance: outbox-retention ticker: %v", err)
				}
			}
		}
	}()
}
