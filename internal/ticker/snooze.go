// Snooze expiry (C2T7, spec §3).
//
// The intake inbox resurfaces snoozed issues at read time
// (IntakeIssue.EffectiveStatus), but the stored state should flip too:
// this ticker rewrites snoozed → pending when snoozed_till passes, so
// the database reflects reality between reads. EffectiveStatus stays as
// the safety net for the window between passes.
//
// In-process by design (same known limitation as the cycle ticker, spec
// §3): one instance only in v1.
package ticker

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/service"
)

// SnoozeTicker expires intake snoozes on an interval. Interval <= 0 means
// the default (1 minute).
type SnoozeTicker struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
}

// expiredSnooze is one intake row the ticker flipped.
type expiredSnooze struct {
	id         string
	issueID    string
	wsSlug     string
	identifier string
}

// ExpireOnce flips one batch: every intake row still snoozed whose
// snoozed_till has passed becomes pending (snoozed_till cleared). Each
// flipped row fans out intake.updated on its project + workspace
// channels so open inboxes refresh without a page reload. Idempotent:
// re-running flips nothing new.
func (t *SnoozeTicker) ExpireOnce(ctx context.Context) error {
	rows, err := t.Pool.Query(ctx,
		`UPDATE intake_issues ii
		    SET status = $1, snoozed_till = NULL, updated_at = now()
		   FROM intake it
		   JOIN projects p ON p.id = it.project_id
		   JOIN workspaces w ON w.id = p.workspace_id
		  WHERE ii.intake_id = it.id
		    AND ii.status = $2
		    AND ii.snoozed_till <= now()
		  RETURNING ii.id::text, ii.issue_id::text, w.slug, p.identifier`,
		service.IntakePending, service.IntakeSnoozed)
	if err != nil {
		return err
	}
	var expired []expiredSnooze
	for rows.Next() {
		var e expiredSnooze
		if err := rows.Scan(&e.id, &e.issueID, &e.wsSlug, &e.identifier); err != nil {
			rows.Close()
			return err
		}
		expired = append(expired, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range expired {
		service.BroadcastIntakeResurfaced(e.wsSlug, e.identifier, e.id, e.issueID)
	}
	return nil
}

// Start launches the interval loop in a goroutine; it stops when ctx is
// cancelled. main.go wires this exactly once.
func (t *SnoozeTicker) Start(ctx context.Context) {
	interval := t.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	go func() {
		log.Printf("glance: snooze ticker started (interval %s)", interval)
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if err := t.ExpireOnce(ctx); err != nil {
					log.Printf("glance: snooze ticker: %v", err)
				}
			}
		}
	}()
}
