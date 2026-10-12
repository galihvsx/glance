package service

// Recurring schedule execution (C28T1, cycle 28 "Recurring work").
//
// This file owns the shared execution path for one schedule occurrence:
// the background ticker (internal/ticker/recurring.go) and the HTTP
// run-now action (a later cycle-28 task) both funnel through
// ExecuteSchedule, so a manual run and a ticker run can never diverge.
//
// Execution model: one occurrence = claim the schedule row (SELECT ...
// FOR UPDATE inside a tx, re-reading the row fresh so a concurrent
// worker serializes on the row lock), validate the actor + references
// at run time, advance next_run_at / run_count (or deactivate), then
// create the issue via the ordinary CreateIssue path so automations,
// webhooks, and Slack fan-out behave exactly like a manual creation.
//
// Stale-reference policy (mirrors ApplyTemplate's TemplateStaleRef, but
// typed for schedules): a label or assignee deleted after the schedule
// was saved — or a created_by who left the workspace (or is NULL) —
// deactivates the schedule (active=false) and returns a
// *ScheduleStaleRef. No infinite failure loop; the schedule shows as
// paused in the UI. Deleted STATES are the exception: migration 000051
// declares state_id ON DELETE SET NULL with the documented "NULL = the
// project's backlog state at run time" semantic, so a deleted state
// degrades gracefully instead of pausing the schedule.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ScheduleStaleRef is returned by ExecuteSchedule when the schedule
// cannot run because a reference went stale after the schedule was
// saved. The schedule is deactivated (active=false) before the error is
// returned, so the ticker never retries a doomed occurrence. Kind is
// one of "label", "assignee", or "actor" ("state" is reserved: deleted
// states degrade to the backlog default via ON DELETE SET NULL, so a
// dangling state_id can only arise outside normal SQL paths).
type ScheduleStaleRef struct {
	Kind string
	ID   string
}

func (e *ScheduleStaleRef) Error() string {
	return fmt.Sprintf("service: recurring schedule references a missing %s: %s", e.Kind, e.ID)
}

// scheduleActivityField is the issue_activities.field value written when
// an issue is created by a schedule run. The `_` prefix follows the
// system-entry convention (_created, _moved, _converted); new_value
// carries {"schedule_id","schedule_name"}.
const scheduleActivityField = "_schedule_created"

// ClaimDueSchedules returns up to limit active schedules whose
// next_run_at has arrived, oldest first, locking the claimed rows with
// FOR UPDATE SKIP LOCKED so concurrent ticker passes never contend on
// the same rows. The caller must run this inside a tx and commit (or
// roll back) promptly: the claim tx must NOT be held across execution —
// ExecuteSchedule updates the same rows on other pool connections, and
// holding the claim open would deadlock. Per-occurrence atomicity lives
// in ExecuteSchedule's own claim (row lock + fresh re-read).
func ClaimDueSchedules(ctx context.Context, tx pgx.Tx, now time.Time, limit int) ([]*RecurringSchedule, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := tx.Query(ctx,
		`SELECT `+scheduleColumns+`
		 FROM issue_schedules
		 WHERE active AND next_run_at <= $1
		 ORDER BY next_run_at
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*RecurringSchedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// deactivateScheduleTx pauses the schedule: active=false, no issue.
func deactivateScheduleTx(ctx context.Context, tx pgx.Tx, scheduleID string) error {
	_, err := tx.Exec(ctx,
		`UPDATE issue_schedules SET active = false, updated_at = now()
		 WHERE id = $1::uuid`, scheduleID)
	return err
}

// staleRefError maps a failed reference check to its typed error.
func staleRefError(kind, id string) *ScheduleStaleRef {
	return &ScheduleStaleRef{Kind: kind, ID: id}
}

// validateScheduleRunRefs re-validates the schedule's actor and
// references at run time against the current workspace state. On any
// stale ref the schedule is deactivated in the same tx and a
// *ScheduleStaleRef is returned.
func validateScheduleRunRefs(ctx context.Context, tx pgx.Tx, wsID string, s *RecurringSchedule) (actorID string, err error) {
	// Actor: the schedule is always attributed to its creator — never to
	// a random user. NULL or non-member creator pauses the schedule.
	if s.CreatedBy == nil || *s.CreatedBy == "" {
		if derr := deactivateScheduleTx(ctx, tx, s.ID); derr != nil {
			return "", derr
		}
		return "", staleRefError("actor", "")
	}
	refs, err := loadScheduleRefsTx(ctx, tx, wsID, s.ProjectID)
	if err != nil {
		return "", err
	}
	if !refs.members[*s.CreatedBy] {
		if derr := deactivateScheduleTx(ctx, tx, s.ID); derr != nil {
			return "", derr
		}
		return "", staleRefError("actor", *s.CreatedBy)
	}
	if s.StateID != nil && !refs.states[*s.StateID] {
		// Defense in depth only: the 000051 FK is ON DELETE SET NULL,
		// so a deleted state arrives here as nil (graceful backlog
		// fallback). A dangling non-nil id can only arise outside
		// normal SQL paths — pausing beats a per-tick 500 loop.
		if derr := deactivateScheduleTx(ctx, tx, s.ID); derr != nil {
			return "", derr
		}
		return "", staleRefError("state", *s.StateID)
	}
	for _, id := range s.LabelIDs {
		if !refs.labels[id] {
			if derr := deactivateScheduleTx(ctx, tx, s.ID); derr != nil {
				return "", derr
			}
			return "", staleRefError("label", id)
		}
	}
	for _, id := range s.AssigneeIDs {
		if !refs.members[id] {
			if derr := deactivateScheduleTx(ctx, tx, s.ID); derr != nil {
				return "", derr
			}
			return "", staleRefError("assignee", id)
		}
	}
	return *s.CreatedBy, nil
}

// loadScheduleRefsTx is loadScheduleRefs over a tx instead of the pool.
func loadScheduleRefsTx(ctx context.Context, tx pgx.Tx, wsID, projID string) (*scheduleRefs, error) {
	refs := &scheduleRefs{
		states:  map[string]bool{},
		labels:  map[string]bool{},
		members: map[string]bool{},
	}
	queries := []struct {
		sql string
		arg string
		set map[string]bool
	}{
		{`SELECT id::text FROM states WHERE project_id = $1::uuid`, projID, refs.states},
		{`SELECT id::text FROM labels WHERE workspace_id = $1::uuid`, wsID, refs.labels},
		{`SELECT user_id::text FROM workspace_members WHERE workspace_id = $1::uuid`, wsID, refs.members},
	}
	for _, qq := range queries {
		rows, err := tx.Query(ctx, qq.sql, qq.arg)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			qq.set[id] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

// resolveScheduleSlugs resolves the workspace slug and project
// identifier CreateIssue/UpdateIssue/AssignAssignee take (they accept
// slugs/identifiers, not UUIDs).
func resolveScheduleSlugs(ctx context.Context, q queryRower, wsID, projID string) (wsSlug, identifier string, err error) {
	if err := q.QueryRow(ctx,
		`SELECT slug FROM workspaces WHERE id = $1::uuid`, wsID).Scan(&wsSlug); err != nil {
		return "", "", err
	}
	if err := q.QueryRow(ctx,
		`SELECT identifier FROM projects WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		projID, wsID).Scan(&identifier); err != nil {
		return "", "", err
	}
	return wsSlug, identifier, nil
}

// ExecuteSchedule executes ONE occurrence of the schedule at runAt,
// returning the created issue. The signature's wsID/projID are UUIDs
// (matching RecurringSchedule.WorkspaceID/ProjectID); actorID is the
// ticker-determined actor and must equal the schedule's creator —
// attribution always stays with created_by.
//
// Claim protocol (the no-double-run guarantee): the schedule row is
// re-read FOR UPDATE in a tx; if the row is inactive, or its
// next_run_at has already advanced past runAt (a concurrent worker ran
// this occurrence), the call is a no-op returning (nil, nil). The row
// lock serializes concurrent executors, so exactly one of them claims
// the occurrence.
//
// Ordering inside: validate refs (stale → deactivate + *ScheduleStaleRef,
// no issue) → exhaustion check (runAt past ends_on → deactivate, no
// issue) → advance the row (the claim) → create the issue via
// CreateIssue (its own tx, so automations/webhooks/Slack fan out exactly
// like a manual creation) → labels → assignees → the schedule-attribution
// activity entry. The claim tx commits before the issue exists: a crash
// between claim and create loses one run rather than duplicating one.
func ExecuteSchedule(ctx context.Context, pool *pgxpool.Pool, wsID, projID string, sched *RecurringSchedule, runAt time.Time, actorID string) (*Issue, error) {
	if sched == nil {
		return nil, fmt.Errorf("%w: nil schedule", ErrInvalidSchedule)
	}
	runAt = runAt.UTC()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Fresh re-read under the row lock: concurrent executors serialize
	// here, and the claim decision below sees committed state.
	fresh, err := scanSchedule(tx.QueryRow(ctx,
		`SELECT `+scheduleColumns+`
		 FROM issue_schedules
		 WHERE id = $1::uuid AND workspace_id = $2::uuid AND project_id = $3::uuid
		 FOR UPDATE`, sched.ID, wsID, projID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScheduleNotFound
		}
		return nil, err
	}
	if !fresh.Active {
		// Deactivated between the ticker's claim and now (or by a
		// concurrent stale-ref path): nothing to do.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	// Already claimed: another worker advanced past this occurrence.
	if fresh.NextRunAt.After(runAt) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}

	actor, err := validateScheduleRunRefs(ctx, tx, wsID, fresh)
	if err != nil {
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, cerr
		}
		return nil, err
	}
	if actorID != "" && actorID != actor {
		// The caller's actor must be the schedule's creator: silently
		// substituting another user would misattribute the issue.
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, cerr
		}
		return nil, staleRefError("actor", actorID)
	}

	// Exhaustion: the due run itself falls past ends_on (ends_on was
	// moved earlier, or the ticker was down). Deactivate, no issue.
	if fresh.EndsOn != nil && dateOnlyUTC(runAt).After(dateOnlyUTC(*fresh.EndsOn)) {
		if err := deactivateScheduleTx(ctx, tx, fresh.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Claim: advance past this occurrence. Zero next = the schedule is
	// exhausted after this run → deactivate, keeping next_run_at (the
	// column is NOT NULL).
	next := fresh.NextOccurrence(runAt.Add(time.Second))
	active := !next.IsZero()
	if !active {
		next = fresh.NextRunAt
	}
	if _, err := tx.Exec(ctx,
		`UPDATE issue_schedules
		 SET next_run_at = $1, last_run_at = $2, run_count = run_count + 1,
		     active = $3, updated_at = now()
		 WHERE id = $4::uuid`,
		next, runAt, active, fresh.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	wsSlug, identifier, err := resolveScheduleSlugs(ctx, pool, wsID, projID)
	if err != nil {
		return nil, err
	}

	title := fresh.RenderTitle(runAt)
	descText := fresh.RenderDescription(runAt)
	var descParam json.RawMessage
	if descText != "" {
		// Plain-text template → the TipTap doc JSON the frontend
		// renders (the one canonical builder, not a copy).
		descParam = TiptapDocFromText(descText)
	}
	priority := fresh.Priority
	iss, err := CreateIssue(ctx, pool, wsSlug, identifier, actor, CreateIssueInput{
		Name:        title,
		Description: descParam,
		Priority:    &priority,
		StateID:     fresh.StateID, // nil → project's backlog state
	})
	if err != nil {
		return nil, err
	}

	// Labels: the new issue starts label-free, so additive assignment is
	// exactly the schedule's set (BulkIssueSet's REPLACE semantics).
	for _, labelID := range fresh.LabelIDs {
		if err := AssignLabel(ctx, pool, wsSlug, identifier, iss.ID, labelID, actor); err != nil {
			return nil, err
		}
	}
	for _, assigneeID := range fresh.AssigneeIDs {
		if err := AssignAssignee(ctx, pool, wsSlug, identifier, iss.ID, assigneeID, actor); err != nil {
			return nil, err
		}
	}

	// Schedule-attribution activity entry, in its own tx with the final
	// state of the run.
	nv, _ := json.Marshal(map[string]string{
		"schedule_id":   fresh.ID,
		"schedule_name": fresh.Name,
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)`,
		iss.ID, actor, scheduleActivityField, string(nv)); err != nil {
		return nil, err
	}

	return iss, nil
}
