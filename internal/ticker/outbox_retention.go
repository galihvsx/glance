// Outbox retention (C57T1).
//
// Once a day the retention pass deletes done/failed outbox rows
// processed longer ago than the retention window (30 days,
// service.OutboxRetentionDays). Pending and delivering rows are
// never touched — the safety lives in service.PruneOutbox's WHERE
// clause, not here. The pass itself lives in service.PruneOutbox;
// this file is only the interval loop.
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

// RunOnce performs one retention pass against the live clock.
func (t *OutboxRetentionTicker) RunOnce(ctx context.Context) error {
	_, err := service.PruneOutbox(ctx, t.Pool, time.Now())
	return err
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
