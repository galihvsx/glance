package ticker

// Recurring issue schedules (C28T1, cycle 28 "Recurring work"): on an
// interval, execute every schedule whose next_run_at has arrived. Plane
// has no native scheduled issue creation; glance ships it free.
//
// In-process by design (same known limitation as the other tickers):
// one instance only in v1. The no-double-run guarantee does NOT depend
// on that: the batch claim uses FOR UPDATE SKIP LOCKED, and each
// occurrence is claimed atomically inside service.ExecuteSchedule (row
// lock + fresh re-read + advance), so even two concurrent passes can
// never create the same occurrence twice.

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// recurringTickerBatchLimit caps one pass: refs are validated at write
// time so per-schedule failures are rare, and the whole batch is cheap.
const recurringTickerBatchLimit = 50

// RecurringTicker executes due recurring schedules on an interval.
// Interval <= 0 means the default (1 minute).
type RecurringTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// RunOnce performs one pass against the given clock. The explicit now
// is what makes this testable without sleeping: production passes
// time.Now(), tests pass a fake.
//
// The due batch is claimed in ONE tx (SELECT ... FOR UPDATE SKIP
// LOCKED) which is committed BEFORE executing: holding the claim tx
// across execution would deadlock, because ExecuteSchedule updates the
// same schedule rows on other pool connections. Per-occurrence
// atomicity lives in ExecuteSchedule's own claim, so a second pass that
// claims a row before the first pass advances it still cannot
// double-create — the loser sees the advanced next_run_at and no-ops.
func (t *RecurringTicker) RunOnce(ctx context.Context, now time.Time) error {
	tx, err := t.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	due, err := service.ClaimDueSchedules(ctx, tx, now, recurringTickerBatchLimit)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for _, s := range due {
		// Attribution always stays with the schedule's creator;
		// ExecuteSchedule re-validates membership and deactivates the
		// schedule on a stale actor rather than misattributing.
		actor := ""
		if s.CreatedBy != nil {
			actor = *s.CreatedBy
		}
		if _, err := service.ExecuteSchedule(ctx, t.Pool, s.WorkspaceID, s.ProjectID, s, s.NextRunAt, actor); err != nil {
			// A stale ref deactivates the schedule — a handled,
			// expected outcome, not a batch failure: keep going.
			var stale *service.ScheduleStaleRef
			if errors.As(err, &stale) {
				log.Printf("glance: recurring ticker: schedule %s paused (%s)", s.ID, stale)
				continue
			}
			// Unexpected error: abort the pass; the next tick retries
			// the remaining due schedules.
			return err
		}
	}
	return nil
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once — a ticker that is built
// but never started is the Task 10 dispatcher bug all over again.
func (t *RecurringTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		log.Printf("glance: recurring ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.RunOnce(ctx, time.Now()); err != nil {
					log.Printf("glance: recurring ticker: %v", err)
				}
			}
		}
	}()
}
