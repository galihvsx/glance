package service

// Project analytics (C4T3): read-only aggregates under
// /api/v1/workspaces/{slug}/projects/{identifier}/analytics.
//
// Conventions (mirror cycles):
//   - Tenancy: every op resolves the project via resolveCycleProject
//     (needMember=false — any workspace role may read; a bad slug or
//     non-member caller surfaces ErrNotFound). No mutations here.
//   - Scope is LIVE issues: deleted_at IS NULL AND archived_at IS NULL
//     (matches the list endpoint's default; archived work is not
//     "current project state").
//   - Day boundaries are UTC midnights, like the cycle burndown.
//   - Estimates come from estimate_points.value via
//     issues.estimate_point_id (NULL = unestimated = 0).

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidAnalyticsDays is returned when the trends `days` query
// param is not a positive integer. The handler maps it to 400
// bad_request.
var ErrInvalidAnalyticsDays = errors.New("service: invalid analytics days")

// liveIssueScope is the WHERE fragment shared by the analytics
// aggregates: live (not soft-deleted, not archived) issues of one
// project. The project_id placeholder is always $1 — every analytics
// query binds the project first.
func liveIssueScope() string {
	return "i.project_id = $1::uuid AND i.deleted_at IS NULL AND i.archived_at IS NULL"
}

// AnalyticsSummary is the project-level aggregate snapshot.
type AnalyticsSummary struct {
	ByState       map[string]int64 `json:"by_state"`
	ByPriority    map[string]int64 `json:"by_priority"`
	ByLabel       map[string]int64 `json:"by_label"`
	OverdueCount  int64            `json:"overdue_count"`
	EstimateTotal int64            `json:"estimate_total"`
	EstimateDone  int64            `json:"estimate_done"`
}

// GetAnalyticsSummary returns live-issue counts by state / priority /
// label, the overdue count (target_date before today, not in a done
// state group), and estimate totals (done = completed/cancelled
// groups). Five small indexed queries — no N+1.
func GetAnalyticsSummary(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*AnalyticsSummary, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}

	s := &AnalyticsSummary{
		ByState:    map[string]int64{},
		ByPriority: map[string]int64{},
		ByLabel:    map[string]int64{},
	}
	scope := "WHERE " + liveIssueScope()

	scanCounts := func(query string, args ...any) (map[string]int64, error) {
		m := map[string]int64{}
		rows, err := pool.Query(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int64
			if err := rows.Scan(&k, &n); err != nil {
				return nil, err
			}
			m[k] = n
		}
		return m, rows.Err()
	}

	if s.ByState, err = scanCounts(
		`SELECT i.state_id::text, COUNT(*) FROM issues i `+scope+` GROUP BY i.state_id`, projectID); err != nil {
		return nil, err
	}
	if s.ByPriority, err = scanCounts(
		`SELECT i.priority::text, COUNT(*) FROM issues i `+scope+` GROUP BY i.priority`, projectID); err != nil {
		return nil, err
	}
	if s.ByLabel, err = scanCounts(
		`SELECT l.id::text, COUNT(*)
		 FROM issue_labels il
		 JOIN labels l ON l.id = il.label_id
		 JOIN issues i ON i.id = il.issue_id AND i.deleted_at IS NULL AND i.archived_at IS NULL
		 WHERE i.project_id = $1::uuid
		 GROUP BY l.id`, projectID); err != nil {
		return nil, err
	}

	doneGroups := append([]string{}, doneStateGroups...)
	err = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues i
		 WHERE `+liveIssueScope()+`
		   AND i.target_date < CURRENT_DATE
		   AND NOT EXISTS (SELECT 1 FROM states s
		                   WHERE s.id = i.state_id AND s."group" = ANY($2))`,
		projectID, doneGroups).Scan(&s.OverdueCount)
	if err != nil {
		return nil, err
	}

	err = pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(ep.value), 0), COALESCE(SUM(CASE WHEN s."group" = ANY($2) THEN ep.value ELSE 0 END), 0)
		 FROM issues i
		 LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id
		 LEFT JOIN states s ON s.id = i.state_id
		 WHERE `+liveIssueScope(),
		projectID, doneGroups).Scan(&s.EstimateTotal, &s.EstimateDone)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// AnalyticsBurndownDay is one day of the cycle burndown. Remaining and
// RemainingEstimate are nil for future days (no actuals yet).
type AnalyticsBurndownDay struct {
	Date              string `json:"date"`
	Remaining         *int   `json:"remaining"`
	RemainingEstimate *int64 `json:"remaining_estimate"`
}

// AnalyticsBurndown is the per-day remaining scope of a cycle.
type AnalyticsBurndown struct {
	StartDate  string                 `json:"start_date"`
	EndDate    string                 `json:"end_date"`
	TotalScope int                    `json:"total_scope"`
	Days       []AnalyticsBurndownDay `json:"days"`
}

// GetAnalyticsCycleBurndown reconstructs per-day remaining issue count
// AND remaining estimate over a cycle's date range.
//
// Honesty notes (same lineage as the C2T4 cycle burndown):
//   - Remaining counts are exact: state_id transitions come from the
//     issue_activities audit log (field='state_id'), bucketed by UTC
//     date; done = completed/cancelled groups. Scope uses current
//     cycle membership; an issue added mid-cycle counts from its
//     cycle_issues.created_at day. Issues removed from the cycle leave
//     no audit row, so a removal shrinks every day retroactively.
//   - Remaining ESTIMATE uses each issue's CURRENT estimate_point
//     value for every day it was remaining. Mid-cycle estimate edits
//     are NOT time-traveled (the audit log does record
//     estimate_point_id changes, but replaying per-day values was
//     judged disproportionate for an analytics view). Unestimated
//     issues contribute 0.
func GetAnalyticsCycleBurndown(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string) (*AnalyticsBurndown, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	c, err := resolveCycle(ctx, pool, projectID, cycleID)
	if err != nil {
		return nil, err
	}

	done := map[string]bool{}
	rows, err := pool.Query(ctx,
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND "group" = ANY($2)`,
		projectID, doneStateGroups)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		done[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type issueRow struct {
		id       string
		stateID  string
		estimate int64
		added    string // YYYY-MM-DD (UTC)
	}
	var issues []issueRow
	ids := []string{}
	rows, err = pool.Query(ctx,
		`SELECT i.id::text, i.state_id::text, COALESCE(ep.value, 0),
		        (ci.created_at AT TIME ZONE 'UTC')::date::text
		 FROM cycle_issues ci
		 JOIN issues i ON i.id = ci.issue_id AND i.deleted_at IS NULL
		 LEFT JOIN estimate_points ep ON ep.id = i.estimate_point_id
		 WHERE ci.cycle_id = $1::uuid`,
		cycleID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r issueRow
		if err := rows.Scan(&r.id, &r.stateID, &r.estimate, &r.added); err != nil {
			rows.Close()
			return nil, err
		}
		issues = append(issues, r)
		ids = append(ids, r.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type transition struct {
		day      string
		oldState string
		newState string
	}
	transByIssue := map[string][]transition{}
	if len(ids) > 0 {
		rows, err = pool.Query(ctx,
			`SELECT issue_id::text, (created_at AT TIME ZONE 'UTC')::date::text,
			        old_value #>> '{}', new_value #>> '{}'
			 FROM issue_activities
			 WHERE issue_id::text = ANY($1) AND field = 'state_id'
			 ORDER BY created_at ASC`,
			ids)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var issueID string
			var tr transition
			if err := rows.Scan(&issueID, &tr.day, &tr.oldState, &tr.newState); err != nil {
				rows.Close()
				return nil, err
			}
			transByIssue[issueID] = append(transByIssue[issueID], tr)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	startDay := c.StartDate.Format("2006-01-02")
	nDays := int(c.EndDate.Sub(c.StartDate).Hours()/24) + 1
	if nDays < 1 {
		nDays = 1
	}
	today := time.Now().UTC().Format("2006-01-02")

	bd := &AnalyticsBurndown{
		StartDate:  startDay,
		EndDate:    c.EndDate.Format("2006-01-02"),
		TotalScope: len(issues),
		Days:       make([]AnalyticsBurndownDay, 0, nDays),
	}
	for i := 0; i < nDays; i++ {
		day := c.StartDate.AddDate(0, 0, i).Format("2006-01-02")
		d := AnalyticsBurndownDay{Date: day}
		if day <= today {
			rem, remEst := 0, int64(0)
			for _, iss := range issues {
				if iss.added > day {
					continue
				}
				// Replay from the pre-first-transition state (the
				// current state_id is only correct for today).
				state := iss.stateID
				if trs := transByIssue[iss.id]; len(trs) > 0 {
					state = trs[0].oldState
				}
				for _, tr := range transByIssue[iss.id] {
					if tr.day > day {
						break
					}
					state = tr.newState
				}
				if !done[state] {
					rem++
					remEst += iss.estimate
				}
			}
			d.Remaining = &rem
			d.RemainingEstimate = &remEst
		}
		bd.Days = append(bd.Days, d)
	}
	return bd, nil
}

// AnalyticsTrendDay is one day of created/closed counts.
type AnalyticsTrendDay struct {
	Date    string `json:"date"`
	Created int64  `json:"created"`
	Closed  int64  `json:"closed"`
}

// GetAnalyticsTrends returns per-day created/closed issue counts for
// the last `days` days (UTC). "Closed" is derived from the audit log:
// the day an issue's state_id transitioned into a done
// (completed/cancelled) group. Issues have no closed_at column, so
// this is the honest source — a transition out of a done group back
// to open counts as a close on the original day only. Days with no
// activity are present with zeros (generate_series).
func GetAnalyticsTrends(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, days int) ([]AnalyticsTrendDay, error) {
	projectID, _, err := resolveCycleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if days < 1 {
		days = 1
	}
	if days > 90 {
		days = 90
	}

	rows, err := pool.Query(ctx,
		`WITH days(d) AS (
		   SELECT (CURRENT_DATE - (s || ' days')::interval)::date
		   FROM generate_series(0, $2 - 1) s
		 ),
		 created AS (
		   SELECT (i.created_at AT TIME ZONE 'UTC')::date AS d, COUNT(*) AS n
		   FROM issues i
		   WHERE i.project_id = $1::uuid AND i.deleted_at IS NULL
		     AND i.created_at >= CURRENT_DATE - ($2 || ' days')::interval
		   GROUP BY 1
		 ),
		 closed AS (
		   SELECT (a.created_at AT TIME ZONE 'UTC')::date AS d, COUNT(DISTINCT a.issue_id) AS n
		   FROM issue_activities a
		   JOIN issues i ON i.id = a.issue_id AND i.project_id = $1::uuid AND i.deleted_at IS NULL
		   JOIN states s ON s.id::text = (a.new_value #>> '{}') AND s.project_id = $1::uuid AND s."group" = ANY($3)
		   WHERE a.field = 'state_id'
		     AND a.created_at >= CURRENT_DATE - ($2 || ' days')::interval
		   GROUP BY 1
		 )
		 SELECT days.d::text, COALESCE(created.n, 0), COALESCE(closed.n, 0)
		 FROM days
		 LEFT JOIN created ON created.d = days.d
		 LEFT JOIN closed ON closed.d = days.d
		 ORDER BY days.d ASC`,
		projectID, days, doneStateGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnalyticsTrendDay{}
	for rows.Next() {
		var t AnalyticsTrendDay
		if err := rows.Scan(&t.Date, &t.Created, &t.Closed); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
