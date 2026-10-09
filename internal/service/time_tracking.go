package service

// Time tracking (C3T7): issue-scoped worklogs. Lean by design: no billing,
// no reports page — just start/stop/log/list per issue plus a total.
//
// Conventions (mirror the issue satellites):
//   - Tenancy: every op resolves workspace membership + project, then the
//     live issue, via resolveSatelliteIssue. A soft-deleted issue 404s.
//   - Roles: any member (guest 5+) may read; mutations need member (15)+
//     via requireSatelliteWriter.
//   - One running timer per user+issue: enforced by the partial unique
//     index uq_time_entries_running, so concurrent double-starts serialize
//     into a conflict the service maps to 409 (never two running rows).
//   - total_seconds covers COMPLETED entries only. A running entry's
//     elapsed time is computed client-side from its started_at (the
//     response carries the entry, so the widget can tick live). This keeps
//     the number deterministic for a given set of completed entries.
//   - No realtime broadcast: timers are personal worklogs; announcing
//     start/stop on the issue channel would leak work patterns to every
//     subscriber. The widget polls via its own query.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrTimerAlreadyRunning is returned when starting a timer while the
	// user already has a running entry on the issue. The handler maps it
	// to 409.
	ErrTimerAlreadyRunning = errors.New("service: timer already running")
	// ErrNoRunningTimer is returned when stopping with no running entry.
	// The handler maps it to 404.
	ErrNoRunningTimer = errors.New("service: no running timer")
	// ErrInvalidTimeEntry is returned for a malformed manual log:
	// unparseable timestamps or ended_at not after started_at. The
	// handler maps it to 400.
	ErrInvalidTimeEntry = errors.New("service: invalid time entry")
)

// TimeEntry is one logged work chunk. EndedAt nil = timer running.
type TimeEntry struct {
	ID        string     `json:"id"`
	IssueID   string     `json:"issue_id"`
	UserID    string     `json:"user_id"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
}

const timeEntryColumns = `id::text, issue_id::text, user_id::text,
	started_at, ended_at, note, created_at`

func scanTimeEntry(row pgx.Row) (TimeEntry, error) {
	var e TimeEntry
	err := row.Scan(&e.ID, &e.IssueID, &e.UserID,
		&e.StartedAt, &e.EndedAt, &e.Note, &e.CreatedAt)
	return e, err
}

// StartTimer starts the caller's timer on the issue. 409 when the caller
// already has a running entry (the partial unique index makes concurrent
// double-starts conflict instead of creating two rows).
func StartTimer(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*TimeEntry, error) {
	_, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	var e TimeEntry
	err = pool.QueryRow(ctx,
		`INSERT INTO time_entries (issue_id, user_id, started_at)
		 VALUES ($1::uuid, $2::uuid, now())
		 ON CONFLICT (issue_id, user_id) WHERE ended_at IS NULL DO NOTHING
		 RETURNING `+timeEntryColumns,
		issueID, actorID).Scan(&e.ID, &e.IssueID, &e.UserID,
		&e.StartedAt, &e.EndedAt, &e.Note, &e.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTimerAlreadyRunning
		}
		return nil, err
	}
	return &e, nil
}

// StopTimer stops the caller's running timer on the issue (ended_at =
// now). 404 when no timer is running.
func StopTimer(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*TimeEntry, error) {
	_, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	var e TimeEntry
	err = pool.QueryRow(ctx,
		`UPDATE time_entries SET ended_at = now()
		 WHERE issue_id = $1::uuid AND user_id = $2::uuid AND ended_at IS NULL
		 RETURNING `+timeEntryColumns,
		issueID, actorID).Scan(&e.ID, &e.IssueID, &e.UserID,
		&e.StartedAt, &e.EndedAt, &e.Note, &e.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoRunningTimer
		}
		return nil, err
	}
	return &e, nil
}

// LogTimeEntry records a manual entry. ended must be after started; the
// DB CHECK enforces it too (defense in depth).
func LogTimeEntry(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, started, ended time.Time, note string) (*TimeEntry, error) {
	if !ended.After(started) {
		return nil, ErrInvalidTimeEntry
	}
	_, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	e, err := scanTimeEntry(pool.QueryRow(ctx,
		`INSERT INTO time_entries (issue_id, user_id, started_at, ended_at, note)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		 RETURNING `+timeEntryColumns,
		issueID, actorID, started.UTC(), ended.UTC(), note))
	if err != nil {
		return nil, err
	}
	return &e, nil
}

// ListTimeEntries returns the issue's entries (newest first) plus
// total_seconds over completed entries. Any member may read.
func ListTimeEntries(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]TimeEntry, int64, error) {
	_, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, 0, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+timeEntryColumns+` FROM time_entries
		 WHERE issue_id = $1::uuid
		 ORDER BY started_at DESC, created_at DESC`,
		issueID)
	if err != nil {
		return nil, 0, err
	}
	entries := []TimeEntry{}
	for rows.Next() {
		e, err := scanTimeEntry(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	err = pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (ended_at - started_at)))::bigint, 0)
		 FROM time_entries
		 WHERE issue_id = $1::uuid AND ended_at IS NOT NULL`,
		issueID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}
