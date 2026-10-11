package ticker

// Scheduled automation triggers (C16T1): on an interval, evaluate the
// scheduled trigger types (issue.due_soon, issue.overdue, issue.stale,
// cycle.ending_soon) of every project's enabled automation rules via
// service.RunScheduledAutomationPass. Same in-process, one-instance
// design as the cycle and backup tickers (spec §3): main.go only builds
// + starts this ticker when GLANCE_AUTOMATION_SCHEDULE_INTERVAL is set;
// an empty interval means the scheduled pass is disabled and
// scheduled-trigger rules never fire.
//
// Dedupe lives in the service layer (automation_runs: one scheduled
// fire per (rule, issue) per calendar day), so a short interval is
// safe — a issue.stale rule cannot comment every 5 minutes.
//
// Overlap guard: a pass can outlast the interval on large instances.
// RunOnce is serialized with an atomic flag — a tick that arrives while
// a pass is in flight is skipped with a log line, never queued, so
// passes can never pile up.

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// AutomationScheduleTicker runs the scheduled-trigger automation pass
// on an interval.
type AutomationScheduleTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration

	running atomic.Bool
}

// RunOnce performs one scheduled-trigger pass. The explicit split from
// Start is what makes this testable without sleeping.
func (t *AutomationScheduleTicker) RunOnce(ctx context.Context) error {
	return service.RunScheduledAutomationPass(ctx, t.Pool, time.Now())
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once, and only when the
// schedule is enabled — a ticker that is built but never started is
// the Task 10 dispatcher bug all over again.
func (t *AutomationScheduleTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		return
	}
	go func() {
		log.Printf("glance: automation schedule ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if t.running.Swap(true) {
					log.Printf("glance: automation schedule ticker: previous pass still running, skipping tick")
					continue
				}
				err := t.RunOnce(ctx)
				t.running.Store(false)
				if err != nil {
					log.Printf("glance: automation schedule ticker: %v", err)
				}
			}
		}
	}()
}
