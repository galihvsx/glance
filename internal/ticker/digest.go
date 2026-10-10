// Daily email digest (C10T2), per-user scheduling (C11T2).
//
// The pass aggregates each digest-opted-in user's last 24h of
// digest-worthy events (issue.assigned, mention, issue.state_changed —
// already recipient-gated at notify time) into ONE "email.digest" outbox
// row, claimed per user per day via digest_watermarks (migration 000036)
// so re-runs are idempotent. The pass itself lives in
// service.RunDigest; this file is only the interval loop.
//
// The pass runs HOURLY, not daily: C11T2's per-user send-after hour pref
// only makes sense with sub-daily passes — a 24h ticker firing at a fixed
// wall-clock time would starve every user whose pref hour is later than
// the pass time (they would never be due). Hourly passes are cheap and
// safe: service.RunDigest gates each user on due-ness (hour + frequency)
// and the digest_watermarks claim keeps concurrent passes from
// double-sending.
//
// In-process by design (same known limitation as the cycle, reminder, and
// stale-nudge tickers, spec §3): one instance only in v1. Delivery prefs
// default to email off for the digest.daily key; users opt in via the
// notifications preferences.
package ticker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// DigestTicker runs the email-digest pass. Interval <= 0 means the
// default (1 hour — the pass is due-ness-gated per user, so most passes
// are no-ops).
type DigestTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one digest pass against the live clock.
func (t *DigestTicker) RunOnce(ctx context.Context) error {
	return service.RunDigest(ctx, t.Pool, time.Now())
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once.
func (t *DigestTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = time.Hour
	}
	go func() {
		log.Printf("glance: digest ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx); err != nil {
					log.Printf("glance: digest ticker: %v", err)
				}
			}
		}
	}()
}
