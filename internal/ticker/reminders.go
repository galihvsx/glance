// Due-date reminders (C8T6, spec §3).
//
// Once a day the reminder pass notifies assignees and watchers about
// issues whose target_date is tomorrow (due_soon, once per issue) and
// overdue issues (overdue, throttled to once per 24h). The pass itself
// lives in service.RunReminders: phase 1 is a single batched scan
// outside any transaction, phase 2 claims each candidate in a short
// per-issue tx (atomic conditional UPSERT on issue_reminders) and
// broadcasts post-commit, so overlapping passes converge instead of
// double-notifying.
//
// In-process by design (same known limitation as the cycle ticker, spec
// §3): one instance only in v1. Delivery prefs default to in_app on /
// email off; users opt into reminder email via the notifications
// preferences (due_soon / overdue event keys).
package ticker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// ReminderTicker runs the daily due-date reminder pass. Interval <= 0
// means the default (24 hours).
type ReminderTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one reminder pass against the live clock.
func (t *ReminderTicker) RunOnce(ctx context.Context) error {
	return service.RunReminders(ctx, t.Pool, time.Now())
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once.
func (t *ReminderTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	go func() {
		log.Printf("glance: reminder ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx); err != nil {
					log.Printf("glance: reminder ticker: %v", err)
				}
			}
		}
	}()
}
