package service

// Capacity-overload alerts (C29T2, cycle 29 "Workload & capacity").
//
// Once a day the capacity pass notifies every workspace member whose
// open estimate-point load exceeds their weekly capacity (the overload
// rule: weekly_capacity_points is non-nil, positive, and open_points >
// weekly_capacity_points — the same rule GetWorkload uses for the
// overloaded flag). The recipient is the overloaded member themself;
// there is no actor, so notifyTx's self-exclusion stays inert.
//
// Dedupe: at most one alert per member per calendar day. The claim is a
// conditional INSERT ... ON CONFLICT (workspace_id, user_id, alert_date)
// DO NOTHING RETURNING on the capacity_alerts table (migration 000053)
// inside the alert tx — two concurrent passes converge on one
// notification per member per day.
//
// Concurrency + N+1: exactly like the reminder job (reminders.go) —
// phase 1 is a single batched scan across ALL workspaces (no tx held),
// phase 2 is one short tx per alerted member. No per-workspace and no
// per-member queries in the scan; only overloaded members ever reach
// phase 2.
//
// Scheduling (no new plumbing): the alerts ride the existing daily
// ReminderTicker pass — ticker.RunOnce calls RunCapacityAlerts after
// RunReminders. Notification delivery honors notification_prefs under
// the capacity.overloaded event key (absent row = defaults: in_app on,
// email off); announceNotifications runs AFTER each commit.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// capacityAlertCandidate is one overloaded (workspace, member) pair from
// the batched scan.
type capacityAlertCandidate struct {
	workspaceID string
	slug        string
	userID      string
	displayName string
	openPoints  int64
	capacity    int
}

// RunCapacityAlerts performs one capacity pass against the given clock.
// The explicit now makes the per-day dedupe deterministic under a test
// clock: production passes time.Now(), tests pass a fake.
func RunCapacityAlerts(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	candidates, err := scanCapacityOverloads(ctx, pool)
	if err != nil {
		return err
	}

	// Phase 2 — per-member short tx: claim today's alert row, write the
	// notification, commit, then broadcast. A claim that loses to a
	// concurrent pass (no RETURNING row) skips silently.
	for _, c := range candidates {
		if err := alertOneMember(ctx, pool, c, now); err != nil {
			return err
		}
	}
	return nil
}

// scanCapacityOverloads runs the single batched scan: every workspace
// member with a positive capacity whose open assigned estimate-point
// load exceeds it. "Open" is the workload definition — live issues
// (deleted_at/archived_at NULL) not in a done state group, via the same
// done-state anti-join as GetWorkload. One query for all workspaces,
// regardless of member count — no N+1.
func scanCapacityOverloads(ctx context.Context, pool *pgxpool.Pool) ([]capacityAlertCandidate, error) {
	rows, err := pool.Query(ctx,
		`SELECT m.workspace_id::text,
		        w.slug,
		        m.user_id::text,
		        COALESCE(u.name, u.email::text),
		        COALESCE(SUM(ep.value) FILTER (WHERE `+workloadOpenCond+`), 0) AS open_points,
		        m.weekly_capacity_points
		   FROM workspace_members m
		   JOIN workspaces w ON w.id = m.workspace_id
		   JOIN users u ON u.id = m.user_id
		   LEFT JOIN issue_assignees ia ON ia.user_id = m.user_id
		   LEFT JOIN issues i ON i.id = ia.issue_id
		                  AND i.deleted_at IS NULL AND i.archived_at IS NULL
		   LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id
		   LEFT JOIN states ds ON ds.id = i.state_id AND ds."group" = ANY($1)
		  WHERE m.weekly_capacity_points > 0
		    AND u.is_active
		  GROUP BY m.workspace_id, w.slug, m.user_id, u.name, u.email, m.weekly_capacity_points
		 HAVING COALESCE(SUM(ep.value) FILTER (WHERE `+workloadOpenCond+`), 0) > m.weekly_capacity_points`,
		doneStateGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []capacityAlertCandidate
	for rows.Next() {
		var c capacityAlertCandidate
		if err := rows.Scan(&c.workspaceID, &c.slug, &c.userID,
			&c.displayName, &c.openPoints, &c.capacity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// alertOneMember claims today's alert row and notifies the member in a
// short tx, then broadcasts post-commit.
func alertOneMember(ctx context.Context, pool *pgxpool.Pool, c capacityAlertCandidate, now time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	claimed, err := claimCapacityAlert(ctx, tx, c.workspaceID, c.userID, now)
	if err != nil {
		return err
	}
	if !claimed {
		// Already alerted today (or a concurrent pass claimed it) —
		// converge silently.
		return nil
	}

	title := fmt.Sprintf("%s is over capacity: %d pts open against %d pts weekly",
		c.displayName, c.openPoints, c.capacity)
	payload := map[string]any{
		"workspace_id":           c.workspaceID,
		"workspace_slug":         c.slug,
		"user_id":                c.userID,
		"open_points":            c.openPoints,
		"weekly_capacity_points": c.capacity,
		"overloaded_by":          c.openPoints - int64(c.capacity),
	}
	// No actor: this is a system job, so the overloaded member is
	// notified — passing "" keeps notifyTx's self-exclusion inert (the
	// same convention as the reminder and stale jobs).
	created, err := notifyTx(ctx, tx, NotifyCapacityOverloaded, title,
		fmt.Sprintf("Open load: %d pts\nWeekly capacity: %d pts", c.openPoints, c.capacity),
		payload, "", []string{c.userID})
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

// claimCapacityAlert atomically claims today's (workspace, member) alert
// row in the caller's tx. The day is written from the job's explicit
// clock, not now(), so the per-day dedupe is deterministic under a test
// clock. Returns false when another pass claimed the row today (or
// already claimed it concurrently) — the PK guard makes concurrent
// passes converge.
func claimCapacityAlert(ctx context.Context, tx pgx.Tx, workspaceID, userID string, now time.Time) (bool, error) {
	day := now.UTC().Truncate(24 * time.Hour)
	var claimed string
	if err := tx.QueryRow(ctx,
		`INSERT INTO capacity_alerts (workspace_id, user_id, alert_date, alerted_at)
		 VALUES ($1::uuid, $2::uuid, $3::date, $4)
		 ON CONFLICT (workspace_id, user_id, alert_date) DO NOTHING
		 RETURNING user_id::text`,
		workspaceID, userID, day, now).Scan(&claimed); err != nil {
		// No RETURNING row = conflict absorbed by the guard → another
		// pass owns today's alert.
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
