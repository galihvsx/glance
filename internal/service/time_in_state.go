package service

// Time-in-state computation (C70T0): "how long a work item has been in
// its current state" (Plane parity). The raw material is the
// issue_activities audit rows with field='state_id' — written for every
// live state change since cycle 47 (MoveIssue remap + DeleteState
// reassign write them in the same tx as the state change, so the rows
// a read sees are always committed).
//
// Design rules (from the cycle-70 plan semantics):
//   - state_entered_at = created_at of the LATEST state_id row for the
//     issue. No such row (last transitioned before cycle 47, or created
//     directly in a state) → issue.created_at, an approximate UPPER
//     BOUND on the true time in state (the state was entered at some
//     unknown point ≥ created_at). The doc says so; the UI renders one
//     duration either way (Plane does the same).
//   - time_in_state = now − state_entered_at, computed server-side at
//     READ time. NO stored column, NO migration, NO background job —
//     YAGNI, same as Plane's live duration.
//   - Terminal states keep counting: no special-casing on done.
//   - Webhooks + realtime broadcasts do NOT carry the derived fields
//     (derived values go stale in payloads): those paths build explicit
//     field maps, never the enriched Issue.
//   - Negative durations (clock-skewed future rows) clamp to 0.
//
// The computation is split into pure functions of (state_id rows,
// issue created_at, now) — frozen-clock testable — plus the read-path
// helpers below: the stateEnteredAtAgg correlated subquery (detail,
// list, and export row queries embed it — same query as the row, no
// extra round-trip, streaming export keeps streaming), populateTimeInState
// (the byte-exact formula applied to one row), and stateTransitionsForIssue
// (the transitions feed's duration input).

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// StateTransition is one issue_activities row with field='state_id':
// the moment the issue entered the row's new-value state. Slices are
// oldest-first (created_at ASC, id ASC — the same total order the
// writers' ORDER BY uses).
type StateTransition struct {
	At time.Time
}

// StateEnteredAt returns when the issue entered its current state:
// created_at of the latest state_id transition, or issueCreatedAt
// when there is none (the pre-history UPPER BOUND fallback).
func StateEnteredAt(transitions []StateTransition, issueCreatedAt time.Time) time.Time {
	if n := len(transitions); n > 0 {
		return transitions[n-1].At
	}
	return issueCreatedAt
}

// TimeInStateSeconds returns now − StateEnteredAt(transitions,
// issueCreatedAt) as whole seconds, clamped at 0 (a future-dated
// activity row yields 0, never a negative duration).
func TimeInStateSeconds(transitions []StateTransition, issueCreatedAt, now time.Time) int64 {
	secs := int64(now.Sub(StateEnteredAt(transitions, issueCreatedAt)).Seconds())
	if secs < 0 {
		return 0
	}
	return secs
}

// TransitionDurations returns, for each transition (oldest first), the
// seconds the issue spent in the state that transition entered: until
// the next transition's created_at, or until now for the latest.
// Byte-exact with the property formula above (the latest entry's
// duration IS the current time_in_state).
func TransitionDurations(transitions []StateTransition, now time.Time) []int64 {
	out := make([]int64, len(transitions))
	for i := range transitions {
		end := now
		if i+1 < len(transitions) {
			end = transitions[i+1].At
		}
		secs := int64(end.Sub(transitions[i].At).Seconds())
		if secs < 0 {
			secs = 0
		}
		out[i] = secs
	}
	return out
}

// stateEnteredAtAgg is the correlated scalar subquery the detail,
// list, and export row queries embed to derive state_entered_at in
// the SAME query as the row (no extra round-trip, and the streaming
// export keeps streaming). It rides the existing
// issue_activities_issue_idx (issue_id, created_at) — no new index, no
// migration — exactly like the assignees/labels/progress correlated
// aggregates already in these queries. NULL when the issue has no
// state_id activity row (the created_at UPPER BOUND fallback).
const stateEnteredAtAgg = `(SELECT MAX(a.created_at)
   FROM issue_activities a
  WHERE a.issue_id = i.id AND a.field = 'state_id')`

// populateTimeInState sets StateEnteredAt + TimeInStateSeconds on one
// issue from the stateEnteredAtAgg subquery value (nil = no row → the
// created_at fallback). now is captured once per request so every row
// of a page shares the same read instant.
func populateTimeInState(iss *Issue, enteredAt *time.Time, now time.Time) {
	var tr []StateTransition
	if enteredAt != nil {
		tr = []StateTransition{{At: *enteredAt}}
	}
	entered := StateEnteredAt(tr, iss.CreatedAt)
	secs := TimeInStateSeconds(tr, iss.CreatedAt, now)
	iss.StateEnteredAt = &entered
	iss.TimeInStateSeconds = &secs
}

// stateTransitionsForIssue reads ALL state_id activity rows for one
// issue, oldest first — the transitions feed's duration input.
func stateTransitionsForIssue(ctx context.Context, pool *pgxpool.Pool, issueID string) ([]StateTransition, error) {
	rows, err := pool.Query(ctx,
		`SELECT a.created_at
		   FROM issue_activities a
		  WHERE a.issue_id = $1::uuid AND a.field = 'state_id'
		  ORDER BY a.created_at ASC, a.id ASC`,
		issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StateTransition{}
	for rows.Next() {
		var tr StateTransition
		if err := rows.Scan(&tr.At); err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
