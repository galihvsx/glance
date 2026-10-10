package ticker

// Scheduled workspace backups (C15T2): on an interval, back up every
// workspace on the instance via service.RunAllBackups (gzipped
// glance-export/1 archives into the configured dir, retention-pruned,
// integrity-verified). Same in-process, one-instance design as the
// cycle ticker (spec §3): main.go only builds + starts this ticker when
// GLANCE_BACKUP_INTERVAL is set; an empty interval means the scheduled
// pass is disabled and backups only happen via
// POST /api/v1/admin/backups/run.
//
// Overlap guard: a backup pass can outlast the interval on large
// instances. RunOnce is serialized with an atomic flag — a tick that
// arrives while a pass is in flight is skipped with a log line, never
// queued, so passes can never pile up.

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// BackupTicker runs the scheduled backup pass on an interval.
type BackupTicker struct {
	Pool      *pgxpool.Pool
	Interval  time.Duration
	Dir       string
	Retention int

	running atomic.Bool
}

// RunOnce performs one backup pass over every workspace. The explicit
// split from Start is what makes this testable without sleeping.
// Per-workspace failures are joined into the returned error; every
// attempted workspace still gets its backup_runs row (ok or failed),
// so a broken workspace never goes silent.
func (t *BackupTicker) RunOnce(ctx context.Context) error {
	runs, err := service.RunAllBackups(ctx, t.Pool, t.Dir, t.Retention, "schedule")
	ok := 0
	for _, r := range runs {
		if r.Status == service.BackupStatusOK {
			ok++
		}
	}
	log.Printf("glance: backup pass finished: %d/%d workspaces ok", ok, len(runs))
	return err
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once, and only when the backup
// schedule is enabled — a ticker that is built but never started is the
// Task 10 dispatcher bug all over again.
func (t *BackupTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		return
	}
	go func() {
		log.Printf("glance: backup ticker started (interval %s, dir %s, retention %d)",
			interval, t.Dir, t.Retention)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if t.running.Swap(true) {
					log.Printf("glance: backup ticker: previous pass still running, skipping tick")
					continue
				}
				err := t.RunOnce(ctx)
				t.running.Store(false)
				if err != nil {
					log.Printf("glance: backup ticker: %v", err)
				}
			}
		}
	}()
}
