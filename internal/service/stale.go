package service

// Stale-issue nudge (C9T5, beyond-parity automation): once a day the stale
// pass notifies assignees and watchers about issues in non-done states
// whose updated_at is older than 30 days — a one-time in-app "stale"
// notification per issue, reset when the issue is updated again.
//
// issue_reminders kind overload: the row is SHARED with the due-date
// reminders (C8T6) — one row per issue, so NO new migration. kind records
// the last reminder kind sent ('due_soon' | 'overdue' | 'stale'). The
// deliberate trade-off of the shared row is that the stale throttle is
// "no reminder of any kind since the issue's last update": a due_soon /
// overdue write after the issue's last update also suppresses the stale
// nudge, and a stale nudge overwrites the row's kind (it cannot resurrect
// an already-fired due_soon claim, which the due_soon scan excludes by
// row presence anyway). This matches C8T6's single-row-per-issue design;
// the acceptance tests pin the required behavior.
//
// Concurrency mirrors RunReminders: the scan is a single batched query
// OUTSIDE any transaction; each candidate is claimed in a short per-issue
// tx with a conditional UPSERT (guarded on
// last_reminded_at < issue.updated_at), so overlapping passes converge
// instead of double-nudging. Delivery prefs default to in_app on / email
// off; users opt into stale email via the "stale" preference key.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StaleAfter is the inactivity threshold for the stale nudge: 30 days.
const StaleAfter = 30 * 24 * time.Hour

// staleCandidate is one issue the scan phase matched.
type staleCandidate struct {
	issueID    string
	identifier string // project identifier, e.g. "GA"
	sequenceID int
	name       string
	updatedAt  time.Time // passed to the claim guard: update resets throttle
}

// RunStaleNudge performs one stale-nudge pass against the given clock.
// The explicit now makes the 30-day threshold deterministic under a test
// clock: production passes time.Now(), tests pass a fake.
func RunStaleNudge(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	cutoff := now.Add(-StaleAfter)

	// Phase 1 — one batched scan, no transaction held. Matches issues
	// untouched for 30+ days in non-done states that have no reminder
	// row, or whose last reminder of ANY kind predates the issue's last
	// update (throttle reset — see the kind-overload comment above).
	// Drafts, archived, deleted, and done-state issues are excluded.
	candidates, err := scanStaleCandidates(ctx, pool, cutoff)
	if err != nil {
		return err
	}

	// Phase 2 — per-issue short tx: claim the reminder row, write the
	// notifications, commit, then broadcast. A claim that loses to a
	// concurrent pass (no RETURNING row) skips silently.
	for _, c := range candidates {
		if err := nudgeOneIssue(ctx, pool, c, now); err != nil {
			return err
		}
	}
	return nil
}

// scanStaleCandidates runs the single batched scan for one pass.
func scanStaleCandidates(ctx context.Context, pool *pgxpool.Pool, cutoff time.Time) ([]staleCandidate, error) {
	rows, err := pool.Query(ctx,
		`SELECT i.id::text, p.identifier, i.sequence_id, i.name, i.updated_at
		   FROM issues i
		   JOIN projects p ON p.id = i.project_id
		   JOIN states s ON s.id = i.state_id
		   LEFT JOIN issue_reminders r ON r.issue_id = i.id
		  WHERE i.updated_at < $1
		    AND i.deleted_at IS NULL
		    AND i.archived_at IS NULL
		    AND NOT i.is_draft
		    AND s."group" NOT IN ('completed', 'cancelled')
		    AND (r.issue_id IS NULL OR r.last_reminded_at < i.updated_at)`,
		cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []staleCandidate
	for rows.Next() {
		var c staleCandidate
		if err := rows.Scan(&c.issueID, &c.identifier, &c.sequenceID, &c.name, &c.updatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// nudgeOneIssue claims the reminder row and notifies recipients in a
// short tx, then broadcasts post-commit.
func nudgeOneIssue(ctx context.Context, pool *pgxpool.Pool, c staleCandidate, now time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	claimed, err := claimStale(ctx, tx, c.issueID, c.updatedAt, now)
	if err != nil {
		return err
	}
	if !claimed {
		// A concurrent pass claimed it first — converge silently.
		return nil
	}

	recipients, err := issueWatchersTx(ctx, tx, c.issueID)
	if err != nil {
		return err
	}
	displayID := fmt.Sprintf("%s-%d", c.identifier, c.sequenceID)
	title := fmt.Sprintf("%s %q has gone stale — no updates in 30 days", displayID, c.name)
	payload := map[string]any{
		"issue_id":   c.issueID,
		"identifier": displayID,
		"name":       c.name,
		"updated_at": c.updatedAt.UTC().Format(time.RFC3339),
		"kind":       NotifyStale,
	}
	// No actor: this is a system job, so every recipient is notified —
	// passing "" keeps notifyTx's self-exclusion inert ("" never matches
	// a real recipient id, which the empty-id filter drops anyway).
	created, err := notifyTx(ctx, tx, NotifyStale, title,
		fmt.Sprintf("Issue: %s\nLast updated: %s", c.name, c.updatedAt.UTC().Format("2006-01-02")),
		payload, "", recipients)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Mention-notification lesson: announce AFTER the commit, never
	// inside the tx.
	announceNotifications(created)
	return nil
}

// claimStale atomically claims the reminder row for one issue in the
// caller's tx. The WHERE guard — no reminder of any kind since the
// issue's last update — makes concurrent passes converge: the loser
// updates zero rows. last_reminded_at is written from the job's explicit
// clock, not now(), so the throttle is deterministic under a test clock.
// Returns false when another pass claimed the row concurrently (or a
// reminder was already sent after the issue's last update).
func claimStale(ctx context.Context, tx pgx.Tx, issueID string, issueUpdatedAt, now time.Time) (bool, error) {
	var claimed string
	if err := tx.QueryRow(ctx,
		`INSERT INTO issue_reminders (issue_id, last_reminded_at, kind)
		        VALUES ($1::uuid, $2, $3)
		       ON CONFLICT (issue_id) DO UPDATE
		          SET last_reminded_at = EXCLUDED.last_reminded_at,
		              kind = EXCLUDED.kind
		        WHERE issue_reminders.last_reminded_at < $4
		     RETURNING issue_id::text`,
		issueID, now, NotifyStale, issueUpdatedAt).Scan(&claimed); err != nil {
		// No RETURNING row = conflict absorbed by the guard → another
		// pass owns this claim.
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
