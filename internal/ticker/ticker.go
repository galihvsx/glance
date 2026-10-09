// Package ticker is the in-process background worker (Task 22, spec §3):
// on an interval it activates upcoming cycles whose start_date has
// arrived and completes ended cycles via service.CompleteCycle (freeze
// the progress snapshot, transfer/detach incomplete issues per the
// project's close_in_days rule).
//
// In-process by design (spec §3 "Known limitation"): one instance only in
// v1. Horizontal scaling later = leader election; not v1.
//
// Snooze expiry lives in SnoozeTicker (snooze.go, C2T7): expired snoozes
// are flipped to pending by a background pass, with the read-time
// EffectiveStatus rule as the safety net.
package ticker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// Ticker runs cycle rollover on an interval. Interval <= 0 means the
// default (1 minute).
type Ticker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one rollover pass against the given clock. The explicit
// now is what makes this testable without sleeping: production passes
// time.Now(), tests pass a fake.
func (t *Ticker) RunOnce(ctx context.Context, now time.Time) error {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	type ended struct{ id, projectID string }

	// Activate: upcoming cycles whose start_date has arrived. RETURNING
	// feeds the realtime fan-out (cycle.updated) via the service layer —
	// the ticker otherwise bypasses service mutations entirely.
	rows, err := t.Pool.Query(ctx,
		`UPDATE cycles SET status = 'current', updated_at = now()
		 WHERE status = 'upcoming' AND start_date <= $1::date
		 RETURNING id::text, project_id::text`, today)
	if err != nil {
		return err
	}
	var activated []ended
	for rows.Next() {
		var a ended
		if err := rows.Scan(&a.id, &a.projectID); err != nil {
			rows.Close()
			return err
		}
		activated = append(activated, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range activated {
		service.BroadcastCycleUpdated(ctx, t.Pool, a.id, a.projectID, "current")
	}

	// Complete: live cycles whose end_date has passed.
	rows, err = t.Pool.Query(ctx,
		`SELECT id::text, project_id::text FROM cycles
		 WHERE status IN ('current', 'upcoming') AND end_date < $1::date`, today)
	if err != nil {
		return err
	}
	var toComplete []ended
	for rows.Next() {
		var e ended
		if err := rows.Scan(&e.id, &e.projectID); err != nil {
			rows.Close()
			return err
		}
		toComplete = append(toComplete, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, e := range toComplete {
		if err := service.CompleteCycle(ctx, t.Pool, e.id, e.projectID, now); err != nil {
			return err
		}
	}

	// Deferred rollover: incomplete issues still attached to completed
	// cycles — the close_in_days grace had not passed at completion, or
	// a next cycle was queued afterwards.
	return service.SweepCompletedCycles(ctx, t.Pool, now)
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once — a ticker that is built but
// never started is the Task 10 dispatcher bug all over again.
func (t *Ticker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		log.Printf("glance: cycle ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx, time.Now()); err != nil {
					log.Printf("glance: cycle ticker: %v", err)
				}
			}
		}
	}()
}
