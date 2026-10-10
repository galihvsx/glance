// Automation run retention (C13T0).
//
// Once a day the retention pass deletes automation_runs rows older than
// each project's retention window (default 90 days, 0 = keep forever,
// per-project via projects.automation_run_retention_days — migration
// 000039, service.PruneAutomationRuns). The pass itself lives in
// service.PruneAutomationRuns; this file is only the interval loop.
//
// Same ethos as the digest scheduling: no HTTP hot path, never inside
// the firing transaction, so a prune failure can never block a state
// change. A failed pass is logged (log.Printf here, slog inside the
// service) and the ticker keeps ticking — never fatal.
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

// RetentionTicker runs the daily automation-run retention prune. Interval
// <= 0 means the default (24 hours).
type RetentionTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one retention pass against the live clock.
func (t *RetentionTicker) RunOnce(ctx context.Context) error {
	_, err := service.PruneAutomationRuns(ctx, t.Pool, time.Now())
	return err
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once.
func (t *RetentionTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		log.Printf("glance: automation-retention ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx); err != nil {
					log.Printf("glance: automation-retention ticker: %v", err)
				}
			}
		}
	}()
}
