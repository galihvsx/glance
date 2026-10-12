package service

// ExecuteSchedule fires ONE occurrence of a recurring schedule
// (cycle 28 "Recurring work"), C28T2 HTTP surface / C28T1 ticker.
//
// Shared contract: executes one occurrence — create the issue from the
// rendered templates, attach labels + assignees, log the _created
// activity, then advance next_run_at — all in ONE transaction. The
// schedule deactivates (active=false) when the occurrences exhaust
// ends_on or when references have gone stale (label deleted,
// assignee no longer a member): a stale-reference deactivation is
// committed and reported as an ErrInvalidSchedule-wrapped error.
//
// Notes:
//   - wsID/projID are the RESOLVED workspace/project UUID strings (the
//     caller — HTTP handler or ticker — resolved tenancy already).
//   - The tx locks the schedule row (FOR UPDATE) so concurrent ticker
//     runs can never fire the same occurrence twice.
//   - The next run is strictly after runAt: NextOccurrence(runAt + 1s).
//   - On exhaustion next_run_at keeps the fired runAt (the last actual
//     occurrence), active flips false.
//   - Issue display_id is filled from the project's identifier; the
//     returned Issue is otherwise the standard issue shape.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExecuteSchedule executes one occurrence of the schedule at runAt.
// It creates the issue, attaches labels + assignees, logs the _created
// activity, and advances next_run_at (deactivating on exhaustion or
// stale references) — all in one transaction. The schedule row is
// re-locked (FOR UPDATE) inside the tx; the passed sched supplies the
// templates and refs.
func ExecuteSchedule(ctx context.Context, pool *pgxpool.Pool, wsID, projID string, sched *RecurringSchedule, runAt time.Time, actorID string) (*Issue, error) {
	if sched == nil {
		return nil, fmt.Errorf("%w: nil schedule", ErrInvalidSchedule)
	}
	if !sched.Active {
		return nil, fmt.Errorf("%w: schedule is not active", ErrInvalidSchedule)
	}
	runAt = runAt.UTC()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Lock the schedule row for this occurrence so two ticker runners
	// cannot fire it twice.
	row, err := scanSchedule(tx.QueryRow(ctx,
		`SELECT `+scheduleColumns+` FROM issue_schedules
		  WHERE id = $1::uuid AND workspace_id = $2::uuid AND project_id = $3::uuid
		  FOR UPDATE`,
		sched.ID, wsID, projID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScheduleNotFound
		}
		return nil, err
	}
	if !row.Active {
		return nil, fmt.Errorf("%w: schedule is not active", ErrInvalidSchedule)
	}

	// Stale references: state (FK SET NULL guards the deleted-state
	// case, but the belt-and-suspenders check is cheap), labels of the
	// workspace, members of the workspace. A mismatch deactivates the
	// schedule and commits — then reports, so the ticker moves on.
	if err := checkExecuteRefs(ctx, tx, wsID, projID, row); err != nil {
		var stale *scheduleStaleRefsError
		if errors.As(err, &stale) {
			if _, uerr := tx.Exec(ctx,
				`UPDATE issue_schedules
				    SET active = false, updated_at = now()
				  WHERE id = $1::uuid`,
				row.ID); uerr != nil {
				return nil, uerr
			}
			if cerr := tx.Commit(ctx); cerr != nil {
				return nil, cerr
			}
			return nil, fmt.Errorf("%w: %s", ErrInvalidSchedule, stale.Message)
		}
		return nil, err
	}

	title := row.RenderTitle(runAt)
	descRendered := row.RenderDescription(runAt)
	var descParam any
	if strings.TrimSpace(descRendered) != "" {
		raw, merr := json.Marshal(descRendered)
		if merr != nil {
			return nil, merr
		}
		descParam = string(raw)
	}

	stateID := ""
	if row.StateID != nil {
		stateID = *row.StateID
	} else {
		stateID, err = backlogState(ctx, tx, projID)
		if err != nil {
			return nil, err
		}
	}

	// Atomic sequence increment — same self-healing pattern as
	// CreateIssue so concurrent fires serialize gapless.
	var seq int
	if err := tx.QueryRow(ctx,
		`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
		 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
		 RETURNING last_value`,
		projID).Scan(&seq); err != nil {
		return nil, fmt.Errorf("service: increment issue sequence: %w", err)
	}

	iss, err := scanIssue(tx.QueryRow(ctx,
		`INSERT INTO issues (project_id, sequence_id, name, description, priority,
			state_id, created_by)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6::uuid, $7::uuid)
		 RETURNING `+issueColumns,
		projID, seq, title, descParam, row.Priority, stateID, actorID))
	if err != nil {
		return nil, err
	}

	for _, lid := range row.LabelIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_labels (issue_id, label_id)
			 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
			iss.ID, lid); err != nil {
			return nil, err
		}
	}
	for _, uid := range row.AssigneeIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id)
			 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
			iss.ID, uid); err != nil {
			return nil, err
		}
	}

	newVal, _ := json.Marshal(map[string]any{"sequence_id": seq, "name": title})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_created', $3::jsonb)`,
		iss.ID, actorID, string(newVal)); err != nil {
		return nil, err
	}

	// Fill the display id from the project identifier (same shape as
	// the normal create path).
	var ident string
	if err := tx.QueryRow(ctx,
		`SELECT identifier FROM projects WHERE id = $1::uuid`, projID).Scan(&ident); err != nil {
		return nil, err
	}
	iss.DisplayID = ident + "-" + strconv.Itoa(iss.SequenceID)

	// Advance: strictly after this run. Zero = exhausted → deactivate,
	// keeping next_run_at at the last actual occurrence.
	next := row.NextOccurrence(runAt.Add(time.Second))
	active := true
	nextRun := next
	if next.IsZero() {
		active = false
		nextRun = runAt
	}
	if _, err := tx.Exec(ctx,
		`UPDATE issue_schedules
		    SET active = $2, next_run_at = $3, last_run_at = $4,
		        run_count = run_count + 1, updated_at = now()
		  WHERE id = $1::uuid`,
		row.ID, active, nextRun, runAt); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return iss, nil
}

// scheduleStaleRefsError marks a stale reference found at execution
// time (a label deleted after the schedule was saved, an assignee who
// left the workspace). The schedule is deactivated; the caller reports
// the wrapped message.
type scheduleStaleRefsError struct {
	Message string
}

func (e *scheduleStaleRefsError) Error() string { return e.Message }

// checkExecuteRefs verifies the schedule's references still resolve
// inside the execution tx: state of the project, labels of the
// workspace, members of the workspace. A missing reference is a
// *scheduleStaleRefsError (the caller deactivates and reports).
func checkExecuteRefs(ctx context.Context, tx pgx.Tx, wsID, projID string, s *RecurringSchedule) error {
	if s.StateID != nil {
		var one int
		if err := tx.QueryRow(ctx,
			`SELECT 1 FROM states WHERE id = $1::uuid AND project_id = $2::uuid`,
			*s.StateID, projID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &scheduleStaleRefsError{Message: fmt.Sprintf("state %q no longer exists", *s.StateID)}
			}
			return err
		}
	}
	if len(s.LabelIDs) > 0 {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM labels
			  WHERE id = ANY($1::uuid[]) AND workspace_id = $2::uuid`,
			s.LabelIDs, wsID).Scan(&n); err != nil {
			return err
		}
		if n != len(s.LabelIDs) {
			return &scheduleStaleRefsError{Message: "a schedule label was deleted"}
		}
	}
	if len(s.AssigneeIDs) > 0 {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM workspace_members
			  WHERE user_id = ANY($1::uuid[]) AND workspace_id = $2::uuid`,
			s.AssigneeIDs, wsID).Scan(&n); err != nil {
			return err
		}
		if n != len(s.AssigneeIDs) {
			return &scheduleStaleRefsError{Message: "a schedule assignee left the workspace"}
		}
	}
	return nil
}
