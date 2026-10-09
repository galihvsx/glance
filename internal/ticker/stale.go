// Stale-issue nudge (C9T5, beyond-parity automation).
//
// Once a day the stale pass notifies assignees and watchers about issues
// in non-done states whose updated_at is older than 30 days — one nudge
// per issue until it is updated again (an update resets the throttle).
// The mark is written to issue_reminders with kind='stale', sharing the
// single row per issue with the due-date reminders (no new migration).
//
// In-process by design (same known limitation as the cycle and reminder
// tickers, spec §3): one instance only in v1. Delivery prefs default to
// in_app on / email off; users opt into stale email via the "stale"
// preference key.
package ticker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// StaleTicker runs the daily stale-issue nudge pass. Interval <= 0 means
// the default (24 hours).
type StaleTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one stale-nudge pass against the live clock.
func (t *StaleTicker) RunOnce(ctx context.Context) error {
	return service.RunStaleNudge(ctx, t.Pool, time.Now())
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once.
func (t *StaleTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		log.Printf("glance: stale-nudge ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx); err != nil {
					log.Printf("glance: stale-nudge ticker: %v", err)
				}
			}
		}
	}()
}
