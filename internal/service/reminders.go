package service

// Due-date reminders (C8T6, spec §3): a daily job notifies assignees and
// watchers about issues whose target_date is tomorrow (due_soon, fired
// once per issue) or already past (overdue, throttled to once per 24h).
//
// Concurrency: the job runs in-process (spec §3 "Known limitation": one
// instance only in v1), but overlapping passes must still converge — a
// slow pass can overlap the next day's. The scan is a single batched
// query OUTSIDE any transaction (no long-held tx); each candidate is then
// claimed in a short per-issue tx with a conditional UPSERT, so two
// concurrent passes converge on one notification. notifyTx writes the
// notification rows (gated by notification_prefs: absent = in_app on,
// email off) and enqueues email.notification outbox rows when the user's
// email pref is on; announceNotifications runs AFTER each commit.
//
// Excluding done-state issues is deliberate: an issue moved to a
// completed/cancelled state no longer needs a reminder (flagged for the
// reviewer).

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reminderCandidate is one issue the scan phase matched.
type reminderCandidate struct {
	issueID    string
	identifier string // project identifier, e.g. "GA"
	sequenceID int
	name       string
	targetDate string // YYYY-MM-DD, DATE column read as text
	kind       string // NotifyDueSoon | NotifyOverdue
}

// RunReminders performs one reminder pass against the given clock.
// The explicit now makes the 24h overdue throttle deterministic under a
// test clock: production passes time.Now(), tests pass a fake.
func RunReminders(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	today := now.UTC().Truncate(24 * time.Hour)
	tomorrow := today.AddDate(0, 0, 1)
	cutoff := now.Add(-24 * time.Hour)

	// Phase 1 — one batched scan, no transaction held. Matches issues
	// due tomorrow that were never reminded, plus overdue issues that
	// were never reminded or last reminded >24h ago. Drafts, archived,
	// deleted, and done-state issues are excluded. target_date is a DATE
	// column; the =/< comparisons are date arithmetic, so the job's
	// calendar day is what matters, not the time zone of now.
	candidates, err := scanReminderCandidates(ctx, pool, today, tomorrow, cutoff)
	if err != nil {
		return err
	}

	// Phase 2 — per-issue short tx: claim the reminder row, write the
	// notifications, commit, then broadcast. A claim that loses to a
	// concurrent pass (no RETURNING row) skips silently.
	for _, c := range candidates {
		if err := remindOneIssue(ctx, pool, c, now); err != nil {
			return err
		}
	}
	return nil
}

// scanReminderCandidates runs the single batched scan for one pass.
func scanReminderCandidates(ctx context.Context, pool *pgxpool.Pool, today, tomorrow, cutoff time.Time) ([]reminderCandidate, error) {
	rows, err := pool.Query(ctx,
		`SELECT i.id::text, p.identifier, i.sequence_id, i.name,
		        i.target_date::text,
		        CASE WHEN i.target_date = $2::date THEN 'due_soon' ELSE 'overdue' END
		   FROM issues i
		   JOIN projects p ON p.id = i.project_id
		   JOIN states s ON s.id = i.state_id
		   LEFT JOIN issue_reminders r ON r.issue_id = i.id
		  WHERE i.target_date IS NOT NULL
		    AND i.deleted_at IS NULL
		    AND i.archived_at IS NULL
		    AND NOT i.is_draft
		    AND s."group" NOT IN ('completed', 'cancelled')
		    AND ((i.target_date = $2::date AND r.issue_id IS NULL)
		      OR (i.target_date < $1::date AND (r.issue_id IS NULL OR r.last_reminded_at <= $3)))`,
		today, tomorrow, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []reminderCandidate
	for rows.Next() {
		var c reminderCandidate
		if err := rows.Scan(&c.issueID, &c.identifier, &c.sequenceID, &c.name, &c.targetDate, &c.kind); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// remindOneIssue claims the reminder row and notifies recipients in a
// short tx, then broadcasts post-commit.
func remindOneIssue(ctx context.Context, pool *pgxpool.Pool, c reminderCandidate, now time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	claimed, err := claimReminder(ctx, tx, c.issueID, c.kind, now)
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

	var eventType, title string
	switch c.kind {
	case NotifyOverdue:
		eventType = NotifyOverdue
		title = fmt.Sprintf("%s %q is overdue", displayID, c.name)
	default:
		eventType = NotifyDueSoon
		title = fmt.Sprintf("%s %q is due tomorrow", displayID, c.name)
	}
	payload := map[string]any{
		"issue_id":    c.issueID,
		"identifier":  displayID,
		"name":        c.name,
		"target_date": c.targetDate,
		"kind":        c.kind,
	}
	// No actor: this is a system job, so every recipient is notified —
	// passing "" keeps notifyTx's self-exclusion inert ("" never matches
	// a real recipient id, which the empty-id filter drops anyway).
	created, err := notifyTx(ctx, tx, eventType, title,
		fmt.Sprintf("Issue: %s\nTarget date: %s", c.name, c.targetDate),
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

// claimReminder atomically claims the reminder row for one issue in the
// caller's tx. last_reminded_at is written from the job's explicit clock,
// not now(), so the 24h overdue throttle is deterministic under a test
// clock. Returns false when another pass claimed the row concurrently
// (or already claimed it within the throttle window).
func claimReminder(ctx context.Context, tx pgx.Tx, issueID, kind string, now time.Time) (bool, error) {
	var sql string
	var args []any
	switch kind {
	case NotifyOverdue:
		// Throttled re-fire: the row is only re-claimed when the
		// previous claim is older than 24h. The WHERE guard makes
		// concurrent passes converge — the loser updates zero rows.
		sql = `INSERT INTO issue_reminders (issue_id, last_reminded_at, kind)
		        VALUES ($1::uuid, $2, $3)
		       ON CONFLICT (issue_id) DO UPDATE
		          SET last_reminded_at = EXCLUDED.last_reminded_at,
		              kind = EXCLUDED.kind
		        WHERE issue_reminders.last_reminded_at <= $4
		     RETURNING issue_id::text`
		args = []any{issueID, now, kind, now.Add(-24 * time.Hour)}
	default:
		// due_soon fires once per issue: never re-claim.
		sql = `INSERT INTO issue_reminders (issue_id, last_reminded_at, kind)
		        VALUES ($1::uuid, $2, $3)
		       ON CONFLICT (issue_id) DO NOTHING
		     RETURNING issue_id::text`
		args = []any{issueID, now, kind}
	}
	var claimed string
	if err := tx.QueryRow(ctx, sql, args...).Scan(&claimed); err != nil {
		// No RETURNING row = conflict absorbed by the guard → another
		// pass owns this claim.
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
