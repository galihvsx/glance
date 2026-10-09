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
	"strings"
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

// TimeSummaryBucket is one aggregation row of the time report.
type TimeSummaryBucket struct {
	Key     string `json:"key"`     // day: "2026-10-09"; week: Monday "2026-10-05"; user/issue: the id
	Label   string `json:"label"`   // day/week: same as key; user: name; issue: "ENG-5"
	Seconds int64  `json:"seconds"` // summed completed durations
	Entries int    `json:"entries"` // number of completed entries
}

// TimeSummary is the project time report (C6T8): completed entries only
// (a running timer's elapsed time stays client-side, same convention as
// ListTimeEntries' total_seconds), grouped by day, week, user, or issue.
type TimeSummary struct {
	Days         int                 `json:"days"`
	GroupBy      string              `json:"group_by"`
	TotalSeconds int64               `json:"total_seconds"`
	Buckets      []TimeSummaryBucket `json:"buckets"`
}

// GetTimeSummary aggregates completed time entries for the project over
// the last `days` days. Any member may read. days clamps to 1..365;
// groupBy is one of day/week/user/issue (anything else → "day").
func GetTimeSummary(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, days int, groupBy string) (*TimeSummary, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}
	switch groupBy {
	case "day", "week", "user", "issue":
	default:
		groupBy = "day"
	}

	sum := &TimeSummary{Days: days, GroupBy: groupBy, Buckets: []TimeSummaryBucket{}}

	// Completed entries only; soft-deleted issues don't count.
	// extraJoin slots the group_by join BEFORE the WHERE clause.
	scope := func(extraJoin string) string {
		return `FROM time_entries te
		JOIN issues i ON i.id = te.issue_id AND i.deleted_at IS NULL
		` + extraJoin + `
		WHERE i.project_id = $1::uuid
		  AND te.ended_at IS NOT NULL
		  AND te.started_at >= now() - make_interval(days => $2)`
	}

	var rows pgx.Rows
	switch groupBy {
	case "day", "week":
		// Zero-filled buckets via generate_series so charts don't gap.
		// Weeks dedupe: several days map to the same Monday.
		trunc := "day"
		if groupBy == "week" {
			trunc = "week"
		}
		rows, err = pool.Query(ctx, `
			WITH buckets(b) AS (
				SELECT DISTINCT date_trunc('`+trunc+`', CURRENT_DATE - (s || ' days')::interval)
				FROM generate_series(0, $2 - 1) s
			)
			SELECT to_char(b.b, 'YYYY-MM-DD'), to_char(b.b, 'YYYY-MM-DD'),
			       COALESCE(agg.seconds, 0), COALESCE(agg.entries, 0)
			FROM buckets b
			LEFT JOIN (
				SELECT date_trunc('`+trunc+`', te.started_at) AS k,
				       SUM(EXTRACT(EPOCH FROM (te.ended_at - te.started_at)))::bigint AS seconds,
				       COUNT(*)::int AS entries
				`+scope("")+`
				GROUP BY k
			) agg ON agg.k = b.b
			ORDER BY b.b`, projectID, days)
	case "user":
		rows, err = pool.Query(ctx, `
			SELECT te.user_id::text,
			       COALESCE(NULLIF(u.name, ''), u.email),
			       SUM(EXTRACT(EPOCH FROM (te.ended_at - te.started_at)))::bigint,
			       COUNT(*)::int
			`+scope("JOIN users u ON u.id = te.user_id")+`
			GROUP BY te.user_id, u.name, u.email
			ORDER BY 3 DESC`, projectID, days)
	case "issue":
		rows, err = pool.Query(ctx, `
			SELECT te.issue_id::text,
			       $3 || '-' || i.sequence_id,
			       SUM(EXTRACT(EPOCH FROM (te.ended_at - te.started_at)))::bigint,
			       COUNT(*)::int
			`+scope("")+`
			GROUP BY te.issue_id, i.sequence_id
			ORDER BY 3 DESC`, projectID, days, strings.ToUpper(identifier))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var b TimeSummaryBucket
		if err := rows.Scan(&b.Key, &b.Label, &b.Seconds, &b.Entries); err != nil {
			return nil, err
		}
		if groupBy == "day" || groupBy == "week" {
			b.Label = b.Key
		}
		sum.Buckets = append(sum.Buckets, b)
		sum.TotalSeconds += b.Seconds
	}
	return sum, rows.Err()
}
